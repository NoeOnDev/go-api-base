// Comando api arranca el servidor HTTP de la aplicación.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/NoeOnDev/go-api-base/internal/config"
	"github.com/NoeOnDev/go-api-base/internal/db"
	"github.com/NoeOnDev/go-api-base/internal/jobs"
	"github.com/NoeOnDev/go-api-base/internal/mail"
	"github.com/NoeOnDev/go-api-base/internal/realtime"
	"github.com/NoeOnDev/go-api-base/internal/secretbox"
	"github.com/NoeOnDev/go-api-base/internal/server"
	"github.com/NoeOnDev/go-api-base/internal/store"
)

func main() {
	var err error

	// Sin argumentos se levanta el servidor. Los subcomandos son tareas de administración
	// que usan la misma configuración y el mismo binario.
	switch {
	case len(os.Args) < 2:
		err = run()
	case os.Args[1] == "create-admin":
		err = runCreateAdmin(os.Args[2:])
	case os.Args[1] == "generate-key":
		err = runGenerateKey()
	case os.Args[1] == "rotate-keys":
		err = runRotateKeys()
	default:
		err = fmt.Errorf("comando desconocido %q (disponibles: create-admin, generate-key, rotate-keys)", os.Args[1])
	}

	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

// run contiene el arranque real. Devolver errores en lugar de llamar a os.Exit
// permite que los defer se ejecuten y facilita probar el arranque.
func run() error {
	// ctx se cancela al recibir Ctrl+C (SIGINT) o la señal de apagado de systemd/Docker (SIGTERM).
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("configuración: %w", err)
	}

	logger := newLogger(cfg)
	slog.SetDefault(logger)
	for _, w := range cfg.Warnings() {
		logger.Warn(w)
	}

	pool, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	if err := db.Migrate(ctx, pool, logger); err != nil {
		return err
	}

	// config.Load ya validó las claves.
	box, err := secretbox.New(cfg.EncryptionKey, cfg.EncryptionPreviousKeys...)
	if err != nil {
		return fmt.Errorf("ENCRYPTION_KEY: %w", err)
	}

	sender, err := newMailSender(cfg, logger)
	if err != nil {
		return err
	}

	// Trabajos en segundo plano: los workers corren en este mismo proceso.
	// Se arrancan sin el ctx de las señales: así la señal no los corta de golpe, y shutdown los
	// detiene con Stop y su propio plazo, dejando terminar lo que está en curso.
	jobsClient, err := jobs.New(pool, logger, jobs.Config{MaxWorkers: cfg.JobsMaxWorkers}, jobs.Deps{
		Queries: store.New(pool),
		Logger:  logger,
		Mailer:  sender,
		Box:     box,
		// Para que la limpieza borre también las sesiones inactivas.
		SessionIdleTimeout: cfg.SessionIdleTimeout,
	})
	if err != nil {
		return err
	}
	if err := jobsClient.Start(context.WithoutCancel(ctx)); err != nil {
		return fmt.Errorf("arrancar los trabajos en segundo plano: %w", err)
	}

	// La app no envía los correos: los encola, y los envía un worker (con reintentos).
	mailer := jobs.NewMailSender(jobsClient, box)

	// Con LISTEN/NOTIFY, los eventos en tiempo real y los cierres de sesión llegan a todas las
	// instancias de la API. Sigue escuchando hasta la señal de apagado.
	broker := realtime.NewBroker()
	if err := broker.Listen(ctx, pool, logger); err != nil {
		return err
	}

	srv := server.New(cfg.HTTPAddr, server.NewHandler(server.Deps{
		Logger: logger,
		DB:     pool,
		Config: cfg,
		Mailer: mailer,
		Broker: broker,
		Jobs:   jobsClient,
		Box:    box,
	}), logger)
	// Al apagar se cierran los streams SSE; si no, Shutdown esperaría hasta el timeout.
	srv.RegisterOnShutdown(broker.Close)

	// El servidor corre en otra goroutine para que esta pueda esperar la señal de apagado.
	serverErr := make(chan error, 1)
	go func() {
		logger.Info("servidor escuchando", "addr", cfg.HTTPAddr, "env", cfg.Env)
		serverErr <- srv.ListenAndServe()
	}()

	select {
	case err := <-serverErr:
		// ListenAndServe solo vuelve antes del apagado si no pudo arrancar (por ejemplo, puerto ocupado).
		return fmt.Errorf("servidor: %w", err)
	case <-ctx.Done():
	}

	// Apagado limpio: deja de aceptar conexiones y espera a que terminen las peticiones en curso
	// y los trabajos que se están ejecutando.
	logger.Info("apagando servidor", "timeout", cfg.ShutdownTimeout.String())
	shutdown(cfg.ShutdownTimeout, srv, jobsClient, logger)
	if err := <-serverErr; !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("servidor: %w", err)
	}

	logger.Info("servidor detenido")
	return nil
}

// stopper es lo que shutdown necesita de los trabajos en segundo plano (el cliente de River).
type stopper interface {
	Stop(ctx context.Context) error
}

// shutdown apaga el servidor HTTP y los trabajos en segundo plano al mismo tiempo, cada uno con
// el timeout completo. Así una petición trabada no les quita tiempo a los trabajos para
// detenerse en orden: uno cortado a mitad de camino River lo reintenta recién a la hora.
//
// Apagarlos a la vez es seguro: una petición que todavía está terminando puede encolar un
// trabajo después de que River dejó de procesar; queda en la base y se ejecuta al arrancar.
//
// No devuelve error: lo que no terminó a tiempo queda en el log.
func shutdown(timeout time.Duration, srv *http.Server, jobs stopper, logger *slog.Logger) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	jobsDone := make(chan error, 1)
	go func() { jobsDone <- jobs.Stop(ctx) }()

	if err := srv.Shutdown(ctx); err != nil {
		// Las conexiones que quedan (por ejemplo, una trabada) se cortan, en lugar de dejarlas a medias.
		logger.Warn("quedaron peticiones en curso al apagar; se cortan", "error", err)
		_ = srv.Close()
	}
	if err := <-jobsDone; err != nil {
		logger.Warn("quedaron trabajos a medio hacer al apagar; River los reintentará en una hora", "error", err)
	}
}

// newMailSender crea el envío de correos según MAIL_DRIVER.
func newMailSender(cfg config.Config, logger *slog.Logger) (mail.Sender, error) {
	if cfg.Mail.Driver == config.MailDriverSMTP {
		s, err := mail.NewSMTPSender(cfg.Mail.SMTPHost, cfg.Mail.SMTPPort, cfg.Mail.SMTPUser, cfg.Mail.SMTPPass, cfg.Mail.From)
		if err != nil {
			return nil, fmt.Errorf("configuración de correo: %w", err)
		}
		return s, nil
	}
	return mail.LogSender{Logger: logger}, nil
}

// newLogger devuelve un logger en texto legible para desarrollo y en JSON para producción,
// que es el formato que esperan las herramientas que procesan logs.
func newLogger(cfg config.Config) *slog.Logger {
	opts := &slog.HandlerOptions{Level: cfg.LogLevel}
	if cfg.IsProduction() {
		return slog.New(slog.NewJSONHandler(os.Stdout, opts))
	}
	return slog.New(slog.NewTextHandler(os.Stdout, opts))
}
