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
	"github.com/NoeOnDev/go-api-base/internal/mail"
	"github.com/NoeOnDev/go-api-base/internal/server"
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

	pool, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	if err := db.Migrate(ctx, pool, logger); err != nil {
		return err
	}

	sender, err := newMailSender(cfg, logger)
	if err != nil {
		return err
	}
	mailer := mail.NewQueue(sender, logger)

	srv := server.New(cfg.HTTPAddr, server.NewHandler(server.Deps{
		Logger: logger,
		DB:     pool,
		Config: cfg,
		Mailer: mailer,
	}), logger)

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

	// Los correos se envían en segundo plano: se espera a que salgan los pendientes.
	if err := mailer.Wait(shutdownCtx); err != nil {
		logger.Warn("quedaron correos sin enviar al apagar", "error", err)
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

	if cfg.IsProduction() {
		logger.Warn("MAIL_DRIVER=log en producción: los correos no se envían, se escriben en el log")
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
