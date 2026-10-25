// Package config carga la configuración de la aplicación desde variables de entorno.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net/mail"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/NoeOnDev/go-api-base/internal/secretbox"
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
	// del cliente viene en X-Forwarded-For. Solo se cree si la conexión viene de localhost
	// o de una red privada (ver httpx.ClientIP).
	TrustProxy bool

	// FrontendURL es la base de los enlaces que van en los correos
	// (por ejemplo https://app.cliente.com/reset-password?token=…).
	FrontendURL string
	// RequireEmailVerification exige que el usuario verifique su email para usar las rutas protegidas.
	RequireEmailVerification bool

	// Mail configura el envío de correos.
	Mail Mail

	// AppName es el nombre de la aplicación, por ejemplo el que muestra la app de autenticación del 2FA.
	AppName string
	// EncryptionKey (32 bytes) cifra los datos sensibles guardados en la base, como el secreto del 2FA.
	// Si se pierde, esos datos no se pueden recuperar.
	EncryptionKey []byte
	// EncryptionPreviousKeys son claves anteriores que se siguen aceptando para leer lo cifrado
	// antes de una rotación (ver docs/encryption.md).
	EncryptionPreviousKeys [][]byte
}

// Drivers de correo soportados.
const (
	MailDriverLog  = "log"
	MailDriverSMTP = "smtp"
)

// Mail es la configuración del envío de correos.
type Mail struct {
	// Driver es "log" (escribe los correos en el log, para desarrollo) o "smtp".
	Driver   string
	From     string
	SMTPHost string
	SMTPPort int
	SMTPUser string
	SMTPPass string
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
func load(rawGetenv func(string) string) (Config, error) {
	getenv := func(key string) string { return unquote(rawGetenv(key)) }

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

	if cfg.FrontendURL, err = baseURL(valueOr(getenv("FRONTEND_URL"), "http://localhost:5173")); err != nil {
		errs = append(errs, fmt.Errorf("FRONTEND_URL: %w", err))
	}

	if cfg.RequireEmailVerification, err = boolOr(getenv("REQUIRE_EMAIL_VERIFICATION"), false); err != nil {
		errs = append(errs, fmt.Errorf("REQUIRE_EMAIL_VERIFICATION %w", err))
	}

	cfg.Mail, err = loadMail(getenv)
	errs = append(errs, err)

	cfg.AppName = valueOr(getenv("APP_NAME"), "Mi App")

	if v := strings.TrimSpace(getenv("ENCRYPTION_KEY")); v == "" {
		errs = append(errs, errors.New("ENCRYPTION_KEY es obligatoria (generala con: make key)"))
	} else if cfg.EncryptionKey, err = secretbox.ParseKey(v); err != nil {
		errs = append(errs, fmt.Errorf("ENCRYPTION_KEY: %w", err))
	}

	for i, v := range strings.Split(getenv("ENCRYPTION_PREVIOUS_KEYS"), ",") {
		if strings.TrimSpace(v) == "" {
			continue
		}
		k, err := secretbox.ParseKey(v)
		if err != nil {
			errs = append(errs, fmt.Errorf("ENCRYPTION_PREVIOUS_KEYS (clave %d): %w", i+1, err))
			continue
		}
		cfg.EncryptionPreviousKeys = append(cfg.EncryptionPreviousKeys, k)
	}

	// Comprueba que el llavero se pueda armar (por ejemplo, que no haya claves repetidas).
	if len(cfg.EncryptionKey) > 0 {
		if _, err := secretbox.New(cfg.EncryptionKey, cfg.EncryptionPreviousKeys...); err != nil {
			errs = append(errs, fmt.Errorf("ENCRYPTION_PREVIOUS_KEYS: %w", err))
		}
	}

	return cfg, errors.Join(errs...)
}

func loadMail(getenv func(string) string) (Mail, error) {
	m := Mail{
		Driver:   valueOr(getenv("MAIL_DRIVER"), MailDriverLog),
		From:     valueOr(getenv("MAIL_FROM"), "no-reply@localhost"),
		SMTPHost: strings.TrimSpace(getenv("SMTP_HOST")),
		SMTPUser: getenv("SMTP_USERNAME"),
		SMTPPass: getenv("SMTP_PASSWORD"),
	}

	var errs []error

	if _, err := mail.ParseAddress(m.From); err != nil {
		errs = append(errs, fmt.Errorf("MAIL_FROM inválido (ejemplo: Mi App <no-reply@miapp.com>): %w", err))
	}

	port, err := strconv.Atoi(valueOr(getenv("SMTP_PORT"), "587"))
	if err != nil || port < 1 || port > 65535 {
		errs = append(errs, errors.New("SMTP_PORT debe ser un número de puerto"))
	}
	m.SMTPPort = port

	switch m.Driver {
	case MailDriverLog:
	case MailDriverSMTP:
		if m.SMTPHost == "" {
			errs = append(errs, errors.New("SMTP_HOST es obligatorio con MAIL_DRIVER=smtp"))
		}
	default:
		errs = append(errs, fmt.Errorf("MAIL_DRIVER debe ser %q o %q, no %q", MailDriverLog, MailDriverSMTP, m.Driver))
	}

	return m, errors.Join(errs...)
}

// baseURL valida una URL base http/https y le quita la barra final.
func baseURL(value string) (string, error) {
	value = strings.TrimSuffix(strings.TrimSpace(value), "/")
	u, err := url.Parse(value)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", fmt.Errorf("%q no es una URL válida (ejemplo: https://app.ejemplo.com)", value)
	}
	return value, nil
}

// unquote quita las comillas que rodean un valor ("Mi App" o 'Mi App'). Así un .env con
// valores entre comillas funciona igual con make (que deja las comillas), con la shell
// (que las quita), con systemd y con Docker.
func unquote(v string) string {
	v = strings.TrimSpace(v)
	if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') && v[len(v)-1] == v[0] {
		return v[1 : len(v)-1]
	}
	return v
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
