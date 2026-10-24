package config

import (
	"fmt"
	"log/slog"
	"testing"
	"time"
)

// testKey es una ENCRYPTION_KEY válida (32 bytes en base64) para los tests.
const testKey = "AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE="

// envFrom simula el entorno. Si el mapa no trae ENCRYPTION_KEY, usa testKey,
// para no repetirla en cada test; para probar su ausencia, pasala vacía.
func envFrom(m map[string]string) func(string) string {
	return func(key string) string {
		if v, ok := m[key]; ok {
			return v
		}
		if key == "ENCRYPTION_KEY" {
			return testKey
		}
		return ""
	}
}

func TestLoadDefaults(t *testing.T) {
	cfg, err := load(envFrom(map[string]string{
		"DATABASE_URL": "postgres://localhost/db",
	}))
	if err != nil {
		t.Fatalf("error inesperado: %v", err)
	}

	if cfg.Env != EnvDevelopment {
		t.Errorf("Env = %q, se esperaba %q", cfg.Env, EnvDevelopment)
	}
	if cfg.HTTPAddr != ":8080" {
		t.Errorf("HTTPAddr = %q, se esperaba %q", cfg.HTTPAddr, ":8080")
	}
	if cfg.LogLevel != slog.LevelInfo {
		t.Errorf("LogLevel = %v, se esperaba %v", cfg.LogLevel, slog.LevelInfo)
	}
	if cfg.ShutdownTimeout != 10*time.Second {
		t.Errorf("ShutdownTimeout = %v, se esperaba %v", cfg.ShutdownTimeout, 10*time.Second)
	}
	if cfg.SessionTTL != 168*time.Hour {
		t.Errorf("SessionTTL = %v", cfg.SessionTTL)
	}
	if cfg.CookieSecure {
		t.Error("CookieSecure debería ser false en desarrollo")
	}
	if cfg.TrustProxy || len(cfg.CORSAllowedOrigins) != 0 {
		t.Errorf("TrustProxy = %v, CORSAllowedOrigins = %v", cfg.TrustProxy, cfg.CORSAllowedOrigins)
	}
	if cfg.FrontendURL != "http://localhost:5173" || cfg.RequireEmailVerification {
		t.Errorf("FrontendURL = %q, RequireEmailVerification = %v", cfg.FrontendURL, cfg.RequireEmailVerification)
	}
	if cfg.Mail.Driver != MailDriverLog || cfg.Mail.SMTPPort != 587 || cfg.Mail.From != "no-reply@localhost" {
		t.Errorf("Mail = %+v", cfg.Mail)
	}
	if cfg.AppName != "Mi App" || len(cfg.EncryptionKey) != 32 {
		t.Errorf("AppName = %q, EncryptionKey = %d bytes", cfg.AppName, len(cfg.EncryptionKey))
	}
}

func TestLoadMailSMTP(t *testing.T) {
	cfg, err := load(envFrom(map[string]string{
		"DATABASE_URL":               "x",
		"FRONTEND_URL":               "https://app.ejemplo.com/",
		"REQUIRE_EMAIL_VERIFICATION": "true",
		"MAIL_DRIVER":                "smtp",
		"MAIL_FROM":                  "Mi App <no-reply@ejemplo.com>",
		"SMTP_HOST":                  "smtp.ejemplo.com",
		"SMTP_PORT":                  "465",
		"SMTP_USERNAME":              "usuario",
		"SMTP_PASSWORD":              "clave",
	}))
	if err != nil {
		t.Fatalf("error inesperado: %v", err)
	}
	if cfg.FrontendURL != "https://app.ejemplo.com" || !cfg.RequireEmailVerification {
		t.Errorf("FrontendURL = %q, RequireEmailVerification = %v", cfg.FrontendURL, cfg.RequireEmailVerification)
	}
	want := Mail{Driver: "smtp", From: "Mi App <no-reply@ejemplo.com>", SMTPHost: "smtp.ejemplo.com", SMTPPort: 465, SMTPUser: "usuario", SMTPPass: "clave"}
	if cfg.Mail != want {
		t.Errorf("Mail = %+v, se esperaba %+v", cfg.Mail, want)
	}
}

func TestLoadCustomValues(t *testing.T) {
	cfg, err := load(envFrom(map[string]string{
		"APP_ENV":              "production",
		"HTTP_ADDR":            "127.0.0.1:9000",
		"DATABASE_URL":         "postgres://localhost/db",
		"LOG_LEVEL":            "debug",
		"SHUTDOWN_TIMEOUT":     "30s",
		"SESSION_TTL":          "24h",
		"TRUST_PROXY":          "true",
		"CORS_ALLOWED_ORIGINS": " https://app.ejemplo.com/ , http://localhost:5173",
	}))
	if err != nil {
		t.Fatalf("error inesperado: %v", err)
	}

	if !cfg.IsProduction() {
		t.Error("IsProduction() = false, se esperaba true")
	}
	if cfg.HTTPAddr != "127.0.0.1:9000" {
		t.Errorf("HTTPAddr = %q", cfg.HTTPAddr)
	}
	if cfg.LogLevel != slog.LevelDebug {
		t.Errorf("LogLevel = %v", cfg.LogLevel)
	}
	if cfg.ShutdownTimeout != 30*time.Second {
		t.Errorf("ShutdownTimeout = %v", cfg.ShutdownTimeout)
	}
	if cfg.SessionTTL != 24*time.Hour {
		t.Errorf("SessionTTL = %v", cfg.SessionTTL)
	}
	if !cfg.CookieSecure {
		t.Error("CookieSecure debería ser true por defecto en producción")
	}
	if !cfg.TrustProxy {
		t.Error("TrustProxy = false")
	}
	want := []string{"https://app.ejemplo.com", "http://localhost:5173"}
	if fmt.Sprint(cfg.CORSAllowedOrigins) != fmt.Sprint(want) {
		t.Errorf("CORSAllowedOrigins = %v, se esperaba %v", cfg.CORSAllowedOrigins, want)
	}
}

func TestCookieSecureOverride(t *testing.T) {
	cfg, err := load(envFrom(map[string]string{
		"APP_ENV":       "production",
		"DATABASE_URL":  "x",
		"COOKIE_SECURE": "false",
	}))
	if err != nil {
		t.Fatalf("error inesperado: %v", err)
	}
	if cfg.CookieSecure {
		t.Error("COOKIE_SECURE=false debería desactivarla aun en producción")
	}
}

func TestLoadInvalid(t *testing.T) {
	tests := map[string]map[string]string{
		"sin DATABASE_URL":   {},
		"APP_ENV inválido":   {"DATABASE_URL": "x", "APP_ENV": "staging"},
		"LOG_LEVEL inválido": {"DATABASE_URL": "x", "LOG_LEVEL": "verbose"},
		"timeout inválido":   {"DATABASE_URL": "x", "SHUTDOWN_TIMEOUT": "diez"},
		"timeout negativo":   {"DATABASE_URL": "x", "SHUTDOWN_TIMEOUT": "-5s"},
	}

	for name, env := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := load(envFrom(env)); err == nil {
				t.Error("se esperaba un error")
			}
		})
	}
}
