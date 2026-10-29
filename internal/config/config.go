// Package config carga la configuración de la aplicación desde variables de entorno.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/mail"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/NoeOnDev/go-api-base/internal/httpx"
	"github.com/NoeOnDev/go-api-base/internal/i18n"
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
	// SessionIdleTimeout cierra una sesión sin actividad durante ese tiempo, además del
	// vencimiento de SessionTTL. 0 lo desactiva.
	SessionIdleTimeout time.Duration
	// CookieSecure hace que la cookie de sesión solo viaje por HTTPS.
	CookieSecure bool
	// CORSAllowedOrigins son los orígenes (por ejemplo https://app.cliente.com) desde los que
	// un frontend en otro dominio puede llamar a la API. Vacío = sin CORS.
	CORSAllowedOrigins []string
	// TrustedProxies son los proxies (por ejemplo Caddy) de los que se acepta X-Forwarded-For,
	// el encabezado con la IP real del cliente. Vacía con TRUST_PROXY=false; con TRUST_PROXY=true,
	// los de TRUSTED_PROXIES o, si no se definen, localhost y las redes privadas
	// (httpx.DefaultTrustedProxies). Ver httpx.ClientIP.
	TrustedProxies httpx.TrustedProxies

	// FrontendURL es la base de los enlaces que van en los correos
	// (por ejemplo https://app.cliente.com/reset-password?token=…).
	FrontendURL string
	// RequireEmailVerification exige que el usuario verifique su email para usar las rutas protegidas.
	RequireEmailVerification bool

	// Mail configura el envío de correos.
	Mail Mail

	// JobsMaxWorkers es cuántos trabajos en segundo plano de la cola default corren a la vez.
	JobsMaxWorkers int

	// AppName es el nombre de la aplicación, por ejemplo el que muestra la app de autenticación del 2FA.
	AppName string
	// DefaultLocale es el idioma de los textos al usuario (errores, correos, notificaciones) cuando
	// la petición no pide uno disponible. Es uno de los catálogos de internal/i18n (ver docs/i18n.md).
	DefaultLocale string
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

// Warnings devuelve las configuraciones válidas pero riesgosas para producción. No son errores,
// porque pueden ser a propósito (por ejemplo, un sistema interno sin HTTPS), pero conviene verlas.
func (c Config) Warnings() []string {
	if !c.IsProduction() {
		return nil
	}
	var w []string
	if !c.CookieSecure {
		w = append(w, "COOKIE_SECURE=false en producción: las cookies viajan sin HTTPS y sin el prefijo __Host-")
	}
	if c.Mail.Driver != MailDriverSMTP {
		w = append(w, "MAIL_DRIVER=log en producción: los correos no se envían, se escriben en el log")
	}
	if u, err := url.Parse(c.FrontendURL); err == nil && isLocalHost(u.Hostname()) {
		w = append(w, "FRONTEND_URL apunta a localhost en producción: los enlaces de los correos (recuperar la contraseña, verificar el email) no van a funcionar")
	}
	if from, err := mail.ParseAddress(c.Mail.From); err == nil && isLocalHost(from.Address[strings.LastIndex(from.Address, "@")+1:]) {
		w = append(w, "MAIL_FROM usa el dominio localhost en producción: los proveedores rechazan esos correos o los mandan a spam")
	}
	return w
}

// isLocalHost indica si host es esta misma máquina: localhost, un subdominio de .localhost o una
// IP de loopback (127.0.0.1, ::1).
func isLocalHost(host string) bool {
	host = strings.ToLower(host)
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
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

	// La última actividad se guarda cada 5 minutos, así que un vencimiento más corto que 15
	// minutos sería impreciso.
	if cfg.SessionIdleTimeout, err = time.ParseDuration(valueOr(getenv("SESSION_IDLE_TIMEOUT"), "0")); err != nil ||
		cfg.SessionIdleTimeout < 0 || (cfg.SessionIdleTimeout > 0 && cfg.SessionIdleTimeout < 15*time.Minute) {
		errs = append(errs, errors.New("SESSION_IDLE_TIMEOUT debe ser 0 (desactivado) o una duración de al menos 15m"))
	}

	// En producción la cookie viaja solo por HTTPS salvo que se indique lo contrario.
	if cfg.CookieSecure, err = boolOr(getenv("COOKIE_SECURE"), cfg.IsProduction()); err != nil {
		errs = append(errs, fmt.Errorf("COOKIE_SECURE %w", err))
	}

	trustProxy, err := boolOr(getenv("TRUST_PROXY"), false)
	if err != nil {
		errs = append(errs, fmt.Errorf("TRUST_PROXY %w", err))
	}
	proxies, err := httpx.ParseTrustedProxies(getenv("TRUSTED_PROXIES"))
	switch {
	case err != nil:
		errs = append(errs, fmt.Errorf("TRUSTED_PROXIES: %w", err))
	case !trustProxy && len(proxies) > 0:
		errs = append(errs, errors.New("TRUSTED_PROXIES requiere TRUST_PROXY=true"))
	case trustProxy && len(proxies) > 0:
		cfg.TrustedProxies = proxies
	case trustProxy:
		cfg.TrustedProxies = httpx.DefaultTrustedProxies
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

	if cfg.JobsMaxWorkers, err = positiveInt(getenv("JOBS_MAX_WORKERS"), 10); err != nil {
		errs = append(errs, fmt.Errorf("JOBS_MAX_WORKERS %w", err))
	}

	cfg.AppName = valueOr(getenv("APP_NAME"), "Mi App")

	cfg.DefaultLocale = valueOr(getenv("DEFAULT_LOCALE"), i18n.BaseLocale)
	if _, err := i18n.New(cfg.DefaultLocale); err != nil {
		errs = append(errs, fmt.Errorf("DEFAULT_LOCALE: %w", err))
	}

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

func positiveInt(value string, fallback int) (int, error) {
	v := strings.TrimSpace(value)
	if v == "" {
		return fallback, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return 0, errors.New("debe ser un número entero positivo")
	}
	return n, nil
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
