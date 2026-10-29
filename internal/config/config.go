// Package config loads the application configuration from environment variables.
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

// Config holds the whole application configuration.
type Config struct {
	Env             string
	HTTPAddr        string
	DatabaseURL     string
	LogLevel        slog.Level
	ShutdownTimeout time.Duration

	// SessionTTL is how long a session lasts from the login.
	SessionTTL time.Duration
	// SessionIdleTimeout closes a session without activity for that long, besides the SessionTTL
	// expiry. 0 turns it off.
	SessionIdleTimeout time.Duration
	// CookieSecure makes the session cookie travel only over HTTPS.
	CookieSecure bool
	// CORSAllowedOrigins are the origins (for example https://app.client.com) from which a
	// frontend on another domain can call the API. Empty = no CORS.
	CORSAllowedOrigins []string
	// TrustedProxies are the proxies (for example Caddy) whose X-Forwarded-For is accepted, the
	// header with the real client IP. Empty with TRUST_PROXY=false; with TRUST_PROXY=true, those of
	// TRUSTED_PROXIES or, if it is not set, localhost and the private networks
	// (httpx.DefaultTrustedProxies). See httpx.ClientIP.
	TrustedProxies httpx.TrustedProxies

	// FrontendURL is the base of the links in the emails
	// (for example https://app.client.com/reset-password?token=…).
	FrontendURL string
	// RequireEmailVerification requires users to verify their email to use the protected routes.
	RequireEmailVerification bool

	// Mail configures sending emails.
	Mail Mail

	// JobsMaxWorkers is how many background jobs of the default queue run at once.
	JobsMaxWorkers int

	// AppName is the application name, for example the one the authenticator app shows for 2FA.
	AppName string
	// DefaultLocale is the language of the user-facing texts (errors, emails, notifications) when
	// the request does not ask for an available one. It is one of the internal/i18n catalogs (see
	// docs/i18n.md).
	DefaultLocale string
	// EncryptionKey (32 bytes) encrypts the sensitive data stored in the database, like the
	// two-factor secret. If it is lost, that data cannot be recovered.
	EncryptionKey []byte
	// EncryptionPreviousKeys are previous keys still accepted to read what was encrypted before a
	// rotation (see docs/encryption.md).
	EncryptionPreviousKeys [][]byte
}

// Supported mail drivers.
const (
	MailDriverLog  = "log"
	MailDriverSMTP = "smtp"
)

// Mail is the configuration for sending emails.
type Mail struct {
	// Driver is "log" (it writes the emails to the log, for development) or "smtp".
	Driver   string
	From     string
	SMTPHost string
	SMTPPort int
	SMTPUser string
	SMTPPass string
}

// IsProduction reports whether the application runs in production.
func (c Config) IsProduction() bool {
	return c.Env == EnvProduction
}

// Warnings returns the settings that are valid but risky for production. They are not errors,
// because they can be on purpose (for example, an internal system without HTTPS), but they are
// worth seeing.
func (c Config) Warnings() []string {
	if !c.IsProduction() {
		return nil
	}
	var w []string
	if !c.CookieSecure {
		w = append(w, "COOKIE_SECURE=false in production: cookies travel without HTTPS and without the __Host- prefix")
	}
	if c.Mail.Driver != MailDriverSMTP {
		w = append(w, "MAIL_DRIVER=log in production: emails are not sent, they are written to the log")
	}
	if u, err := url.Parse(c.FrontendURL); err == nil && isLocalHost(u.Hostname()) {
		w = append(w, "FRONTEND_URL points to localhost in production: the links in the emails (password reset, email verification) will not work")
	}
	if from, err := mail.ParseAddress(c.Mail.From); err == nil && isLocalHost(from.Address[strings.LastIndex(from.Address, "@")+1:]) {
		w = append(w, "MAIL_FROM uses the localhost domain in production: providers reject those emails or send them to spam")
	}
	return w
}

// isLocalHost reports whether host is this same machine: localhost, a .localhost subdomain or a
// loopback IP (127.0.0.1, ::1).
func isLocalHost(host string) bool {
	host = strings.ToLower(host)
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// Load reads the configuration from the environment, applies defaults and validates it.
// It returns every error together, so they can all be fixed at once.
func Load() (Config, error) {
	return load(os.Getenv)
}

// load takes the function that reads variables, so it can be tested without touching the real
// environment.
func load(rawGetenv func(string) string) (Config, error) {
	getenv := func(key string) string { return unquote(rawGetenv(key)) }

	cfg := Config{
		Env:         valueOr(getenv("APP_ENV"), EnvDevelopment),
		HTTPAddr:    valueOr(getenv("HTTP_ADDR"), ":8080"),
		DatabaseURL: getenv("DATABASE_URL"),
	}

	var errs []error

	if cfg.Env != EnvDevelopment && cfg.Env != EnvProduction {
		errs = append(errs, fmt.Errorf("APP_ENV must be %q or %q, not %q", EnvDevelopment, EnvProduction, cfg.Env))
	}

	if cfg.DatabaseURL == "" {
		errs = append(errs, errors.New("DATABASE_URL is required"))
	}

	if err := cfg.LogLevel.UnmarshalText([]byte(valueOr(getenv("LOG_LEVEL"), "info"))); err != nil {
		errs = append(errs, fmt.Errorf("invalid LOG_LEVEL: %w", err))
	}

	var err error
	if cfg.ShutdownTimeout, err = positiveDuration(getenv("SHUTDOWN_TIMEOUT"), "10s"); err != nil {
		errs = append(errs, fmt.Errorf("SHUTDOWN_TIMEOUT %w", err))
	}

	if cfg.SessionTTL, err = positiveDuration(getenv("SESSION_TTL"), "168h"); err != nil {
		errs = append(errs, fmt.Errorf("SESSION_TTL %w", err))
	}

	// The last activity is saved every 5 minutes, so an expiry shorter than 15 minutes would be
	// inaccurate.
	if cfg.SessionIdleTimeout, err = time.ParseDuration(valueOr(getenv("SESSION_IDLE_TIMEOUT"), "0")); err != nil ||
		cfg.SessionIdleTimeout < 0 || (cfg.SessionIdleTimeout > 0 && cfg.SessionIdleTimeout < 15*time.Minute) {
		errs = append(errs, errors.New("SESSION_IDLE_TIMEOUT must be 0 (off) or a duration of at least 15m"))
	}

	// In production the cookie travels only over HTTPS unless told otherwise.
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

	cfg.AppName = valueOr(getenv("APP_NAME"), "My App")

	cfg.DefaultLocale = valueOr(getenv("DEFAULT_LOCALE"), i18n.BaseLocale)
	if _, err := i18n.New(cfg.DefaultLocale); err != nil {
		errs = append(errs, fmt.Errorf("DEFAULT_LOCALE: %w", err))
	}

	if v := strings.TrimSpace(getenv("ENCRYPTION_KEY")); v == "" {
		errs = append(errs, errors.New("ENCRYPTION_KEY is required (generate it with: make key)"))
	} else if cfg.EncryptionKey, err = secretbox.ParseKey(v); err != nil {
		errs = append(errs, fmt.Errorf("ENCRYPTION_KEY: %w", err))
	}

	for i, v := range strings.Split(getenv("ENCRYPTION_PREVIOUS_KEYS"), ",") {
		if strings.TrimSpace(v) == "" {
			continue
		}
		k, err := secretbox.ParseKey(v)
		if err != nil {
			errs = append(errs, fmt.Errorf("ENCRYPTION_PREVIOUS_KEYS (key %d): %w", i+1, err))
			continue
		}
		cfg.EncryptionPreviousKeys = append(cfg.EncryptionPreviousKeys, k)
	}

	// Check that the key ring can be built (for example, that there are no repeated keys).
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
		errs = append(errs, fmt.Errorf("invalid MAIL_FROM (example: My App <no-reply@myapp.com>): %w", err))
	}

	port, err := strconv.Atoi(valueOr(getenv("SMTP_PORT"), "587"))
	if err != nil || port < 1 || port > 65535 {
		errs = append(errs, errors.New("SMTP_PORT must be a port number"))
	}
	m.SMTPPort = port

	switch m.Driver {
	case MailDriverLog:
	case MailDriverSMTP:
		if m.SMTPHost == "" {
			errs = append(errs, errors.New("SMTP_HOST is required with MAIL_DRIVER=smtp"))
		}
	default:
		errs = append(errs, fmt.Errorf("MAIL_DRIVER must be %q or %q, not %q", MailDriverLog, MailDriverSMTP, m.Driver))
	}

	return m, errors.Join(errs...)
}

// baseURL validates an http/https base URL and removes the trailing slash.
func baseURL(value string) (string, error) {
	value = strings.TrimSuffix(strings.TrimSpace(value), "/")
	u, err := url.Parse(value)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", fmt.Errorf("%q is not a valid URL (example: https://app.example.com)", value)
	}
	return value, nil
}

// unquote removes the quotes around a value ("My App" or 'My App'). That way a .env with quoted
// values works the same with make (which keeps the quotes), with the shell (which removes
// them), with systemd and with Docker.
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
		return 0, errors.New("must be a positive duration, like 10s or 168h")
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
		return 0, errors.New("must be a positive whole number")
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
		return false, errors.New("must be true or false")
	}
	return b, nil
}

// origins splits a comma-separated list of origins and checks that each one is scheme + host
// (+ port), without a path: that is how the browser sends the Origin header.
func origins(value string) ([]string, error) {
	var list []string
	for item := range strings.SplitSeq(value, ",") {
		item = strings.TrimSuffix(strings.TrimSpace(item), "/")
		if item == "" {
			continue
		}
		u, err := url.Parse(item)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.Path != "" {
			return nil, fmt.Errorf("%q is not a valid origin (example: https://app.example.com)", item)
		}
		list = append(list, item)
	}
	return list, nil
}
