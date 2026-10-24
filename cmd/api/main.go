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
	"github.com/NoeOnDev/go-api-base/internal/server"
)

func main() {
	if err := run(); err != nil {
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

	srv := server.New(cfg.HTTPAddr, server.NewHandler(server.Deps{
		Logger: logger,
		DB:     pool,
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

	logger.Info("servidor detenido")
	return nil
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
