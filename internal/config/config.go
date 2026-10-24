// Package config carga la configuración de la aplicación desde variables de entorno.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strconv"
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

	// SessionTTL es cuánto dura una sesión desde el login.
	SessionTTL time.Duration
	// CookieSecure hace que la cookie de sesión solo viaje por HTTPS.
	CookieSecure bool
	// CORSAllowedOrigins son los orígenes (por ejemplo https://app.cliente.com) desde los que
	// un frontend en otro dominio puede llamar a la API. Vacío = sin CORS.
	CORSAllowedOrigins []string
	// TrustProxy indica que hay un proxy (por ejemplo Caddy) delante, y que la IP real
	// del cliente viene en X-Forwarded-For.
	TrustProxy bool
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

	var err error
	if cfg.ShutdownTimeout, err = positiveDuration(getenv("SHUTDOWN_TIMEOUT"), "10s"); err != nil {
		errs = append(errs, fmt.Errorf("SHUTDOWN_TIMEOUT %w", err))
	}

	if cfg.SessionTTL, err = positiveDuration(getenv("SESSION_TTL"), "168h"); err != nil {
		errs = append(errs, fmt.Errorf("SESSION_TTL %w", err))
	}

	// En producción la cookie viaja solo por HTTPS salvo que se indique lo contrario.
	if cfg.CookieSecure, err = boolOr(getenv("COOKIE_SECURE"), cfg.IsProduction()); err != nil {
		errs = append(errs, fmt.Errorf("COOKIE_SECURE %w", err))
	}

	if cfg.TrustProxy, err = boolOr(getenv("TRUST_PROXY"), false); err != nil {
		errs = append(errs, fmt.Errorf("TRUST_PROXY %w", err))
	}

	if cfg.CORSAllowedOrigins, err = origins(getenv("CORS_ALLOWED_ORIGINS")); err != nil {
		errs = append(errs, fmt.Errorf("CORS_ALLOWED_ORIGINS: %w", err))
	}

	return cfg, errors.Join(errs...)
}

func valueOr(value, fallback string) string {
	if v := strings.TrimSpace(value); v != "" {
		return v
	}
	return fallback
}

func positiveDuration(value, fallback string) (time.Duration, error) {
	d, err := time.ParseDuration(valueOr(value, fallback))
	if err != nil || d <= 0 {
		return 0, errors.New("debe ser una duración positiva, como 10s o 168h")
	}
	return d, nil
}

func boolOr(value string, fallback bool) (bool, error) {
	v := strings.TrimSpace(value)
	if v == "" {
		return fallback, nil
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return false, errors.New("debe ser true o false")
	}
	return b, nil
}

// origins separa una lista de orígenes por comas y comprueba que cada uno sea
// esquema + host (+ puerto), sin ruta: así es como el navegador envía el encabezado Origin.
func origins(value string) ([]string, error) {
	var list []string
	for item := range strings.SplitSeq(value, ",") {
		item = strings.TrimSuffix(strings.TrimSpace(item), "/")
		if item == "" {
			continue
		}
		u, err := url.Parse(item)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.Path != "" {
			return nil, fmt.Errorf("%q no es un origen válido (ejemplo: https://app.ejemplo.com)", item)
		}
		list = append(list, item)
	}
	return list, nil
}
