// Comando api arranca el servidor HTTP de la aplicación.
package main

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/NoeOnDev/go-api-base/internal/config"
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
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("configuración: %w", err)
	}

	logger := newLogger(cfg)
	slog.SetDefault(logger)

	logger.Info("configuración cargada", "env", cfg.Env, "addr", cfg.HTTPAddr)
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
