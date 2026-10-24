// Package config carga la configuración de la aplicación desde variables de entorno.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"
)

// Entornos soportados.
const (
	EnvDevelopment = "development"
	EnvProduction  = "production"
)

// Config contiene toda la configuración de la aplicación.
type Config struct {
	Env             string
	HTTPAddr        string
	DatabaseURL     string
	LogLevel        slog.Level
	ShutdownTimeout time.Duration
}

// IsProduction indica si la aplicación corre en producción.
func (c Config) IsProduction() bool {
	return c.Env == EnvProduction
}

// Load lee la configuración del entorno, aplica valores por defecto y la valida.
// Devuelve todos los errores juntos para poder corregirlos de una sola vez.
func Load() (Config, error) {
	return load(os.Getenv)
}

// load recibe la función que lee variables para poder probarla sin tocar el entorno real.
func load(getenv func(string) string) (Config, error) {
	cfg := Config{
		Env:         valueOr(getenv("APP_ENV"), EnvDevelopment),
		HTTPAddr:    valueOr(getenv("HTTP_ADDR"), ":8080"),
		DatabaseURL: getenv("DATABASE_URL"),
	}

	var errs []error

	if cfg.Env != EnvDevelopment && cfg.Env != EnvProduction {
		errs = append(errs, fmt.Errorf("APP_ENV debe ser %q o %q, no %q", EnvDevelopment, EnvProduction, cfg.Env))
	}

	if cfg.DatabaseURL == "" {
		errs = append(errs, errors.New("DATABASE_URL es obligatoria"))
	}

	if err := cfg.LogLevel.UnmarshalText([]byte(valueOr(getenv("LOG_LEVEL"), "info"))); err != nil {
		errs = append(errs, fmt.Errorf("LOG_LEVEL inválido: %w", err))
	}

	timeout, err := time.ParseDuration(valueOr(getenv("SHUTDOWN_TIMEOUT"), "10s"))
	if err != nil || timeout <= 0 {
		errs = append(errs, fmt.Errorf("SHUTDOWN_TIMEOUT debe ser una duración positiva, como 10s"))
	}
	cfg.ShutdownTimeout = timeout

	return cfg, errors.Join(errs...)
}

func valueOr(value, fallback string) string {
	if v := strings.TrimSpace(value); v != "" {
		return v
	}
	return fallback
}
