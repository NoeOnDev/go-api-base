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
	// Se arrancan sin el ctx de las señales para apagarlos en orden, después del servidor HTTP.
	jobsClient, err := jobs.New(pool, logger, jobs.Config{MaxWorkers: cfg.JobsMaxWorkers}, jobs.Deps{
		Queries: store.New(pool),
		Logger:  logger,
		Mailer:  sender,
		Box:     box,
	})
	if err != nil {
		return err
	}
	if err := jobsClient.Start(context.WithoutCancel(ctx)); err != nil {
		return fmt.Errorf("arrancar los trabajos en segundo plano: %w", err)
	}

	// La app no envía los correos: los encola, y los envía un worker (con reintentos).
	mailer := jobs.NewMailSender(jobsClient, box)

	broker := realtime.NewBroker()

	srv := server.New(cfg.HTTPAddr, server.NewHandler(server.Deps{
		Logger: logger,
		DB:     pool,
		Config: cfg,
		Mailer: mailer,
		Broker: broker,
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

	// Apagado limpio: deja de aceptar conexiones y espera a que terminen las peticiones en curso.
	logger.Info("apagando servidor", "timeout", cfg.ShutdownTimeout.String())
	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("apagado: %w", err)
	}
	if err := <-serverErr; !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("servidor: %w", err)
	}

	// Los trabajos en curso (incluidos los correos) tienen hasta el mismo timeout para terminar.
	// Si no llegan, quedan en la base y River los vuelve a intentar al arrancar: no se pierden.
	if err := jobsClient.Stop(shutdownCtx); err != nil {
		logger.Warn("quedaron trabajos en curso al apagar; se reintentarán", "error", err)
	}

	logger.Info("servidor detenido")
	return nil
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
