package config

import (
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/NoeOnDev/go-api-base/internal/httpx"
)

// testKey is a valid ENCRYPTION_KEY (32 bytes in base64) for the tests.
const testKey = "AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE="

// envFrom fakes the environment. If the map has no ENCRYPTION_KEY, it uses testKey, to avoid
// repeating it in every test; to test its absence, pass it empty.
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
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Env != EnvDevelopment {
		t.Errorf("Env = %q, want %q", cfg.Env, EnvDevelopment)
	}
	if cfg.HTTPAddr != ":8080" {
		t.Errorf("HTTPAddr = %q, want %q", cfg.HTTPAddr, ":8080")
	}
	if cfg.LogLevel != slog.LevelInfo {
		t.Errorf("LogLevel = %v, want %v", cfg.LogLevel, slog.LevelInfo)
	}
	if cfg.ShutdownTimeout != 10*time.Second {
		t.Errorf("ShutdownTimeout = %v, want %v", cfg.ShutdownTimeout, 10*time.Second)
	}
	if cfg.SessionTTL != 168*time.Hour {
		t.Errorf("SessionTTL = %v", cfg.SessionTTL)
	}
	if cfg.JobsMaxWorkers != 10 {
		t.Errorf("JobsMaxWorkers = %d", cfg.JobsMaxWorkers)
	}
	if cfg.CookieSecure {
		t.Error("CookieSecure should be false in development")
	}
	if len(cfg.TrustedProxies) != 0 || len(cfg.CORSAllowedOrigins) != 0 {
		t.Errorf("TrustedProxies = %v, CORSAllowedOrigins = %v", cfg.TrustedProxies, cfg.CORSAllowedOrigins)
	}
	if cfg.FrontendURL != "http://localhost:5173" || cfg.RequireEmailVerification {
		t.Errorf("FrontendURL = %q, RequireEmailVerification = %v", cfg.FrontendURL, cfg.RequireEmailVerification)
	}
	if cfg.Mail.Driver != MailDriverLog || cfg.Mail.SMTPPort != 587 || cfg.Mail.From != "no-reply@localhost" {
		t.Errorf("Mail = %+v", cfg.Mail)
	}
	if cfg.AppName != "My App" || len(cfg.EncryptionKey) != 32 {
		t.Errorf("AppName = %q, EncryptionKey = %d bytes", cfg.AppName, len(cfg.EncryptionKey))
	}
}

func TestLoadMailSMTP(t *testing.T) {
	cfg, err := load(envFrom(map[string]string{
		"DATABASE_URL":               "x",
		"FRONTEND_URL":               "https://app.example.com/",
		"REQUIRE_EMAIL_VERIFICATION": "true",
		"MAIL_DRIVER":                "smtp",
		"MAIL_FROM":                  "My App <no-reply@example.com>",
		"SMTP_HOST":                  "smtp.example.com",
		"SMTP_PORT":                  "465",
		"SMTP_USERNAME":              "user",
		"SMTP_PASSWORD":              "secret",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.FrontendURL != "https://app.example.com" || !cfg.RequireEmailVerification {
		t.Errorf("FrontendURL = %q, RequireEmailVerification = %v", cfg.FrontendURL, cfg.RequireEmailVerification)
	}
	want := Mail{Driver: "smtp", From: "My App <no-reply@example.com>", SMTPHost: "smtp.example.com", SMTPPort: 465, SMTPUser: "user", SMTPPass: "secret"}
	if cfg.Mail != want {
		t.Errorf("Mail = %+v, want %+v", cfg.Mail, want)
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
		"CORS_ALLOWED_ORIGINS": " https://app.example.com/ , http://localhost:5173",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !cfg.IsProduction() {
		t.Error("IsProduction() = false, want true")
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
		t.Error("CookieSecure should be true by default in production")
	}
	if fmt.Sprint(cfg.TrustedProxies) != fmt.Sprint(httpx.DefaultTrustedProxies) {
		t.Errorf("with TRUST_PROXY=true and without TRUSTED_PROXIES: %v", cfg.TrustedProxies)
	}
	want := []string{"https://app.example.com", "http://localhost:5173"}
	if fmt.Sprint(cfg.CORSAllowedOrigins) != fmt.Sprint(want) {
		t.Errorf("CORSAllowedOrigins = %v, want %v", cfg.CORSAllowedOrigins, want)
	}
}

func TestCookieSecureOverride(t *testing.T) {
	cfg, err := load(envFrom(map[string]string{
		"APP_ENV":       "production",
		"DATABASE_URL":  "x",
		"COOKIE_SECURE": "false",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.CookieSecure {
		t.Error("COOKIE_SECURE=false should turn it off even in production")
	}
}

func TestLoadInvalid(t *testing.T) {
	tests := map[string]map[string]string{
		"without DATABASE_URL": {},
		"invalid APP_ENV":      {"DATABASE_URL": "x", "APP_ENV": "staging"},
		"invalid LOG_LEVEL":    {"DATABASE_URL": "x", "LOG_LEVEL": "verbose"},
		"invalid timeout":      {"DATABASE_URL": "x", "SHUTDOWN_TIMEOUT": "ten"},
		"negative timeout":     {"DATABASE_URL": "x", "SHUTDOWN_TIMEOUT": "-5s"},
		"workers en cero":      {"DATABASE_URL": "x", "JOBS_MAX_WORKERS": "0"},
		"invalid workers":      {"DATABASE_URL": "x", "JOBS_MAX_WORKERS": "ten"},
	}

	for name, env := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := load(envFrom(env)); err == nil {
				t.Error("want an error")
			}
		})
	}
}

func TestLoadUnquotesValues(t *testing.T) {
	cfg, err := load(envFrom(map[string]string{
		"DATABASE_URL": `"postgres://localhost/db"`,
		"APP_NAME":     `"My App"`,
		"MAIL_FROM":    `'My App <no-reply@example.com>'`,
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.AppName != "My App" || cfg.Mail.From != "My App <no-reply@example.com>" || cfg.DatabaseURL != "postgres://localhost/db" {
		t.Errorf("quoted values: AppName=%q, From=%q, DatabaseURL=%q", cfg.AppName, cfg.Mail.From, cfg.DatabaseURL)
	}
}

func TestLoadPreviousKeys(t *testing.T) {
	const prev1 = "AgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgI="
	const prev2 = "AwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwM="
	cfg, err := load(envFrom(map[string]string{
		"DATABASE_URL":             "x",
		"ENCRYPTION_PREVIOUS_KEYS": " " + prev1 + " , " + prev2 + ",",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.EncryptionPreviousKeys) != 2 {
		t.Errorf("want 2 previous keys, got %d", len(cfg.EncryptionPreviousKeys))
	}
}

func TestProductionWarnings(t *testing.T) {
	risky, err := load(envFrom(map[string]string{
		"APP_ENV":       "production",
		"DATABASE_URL":  "x",
		"COOKIE_SECURE": "false",
	}))
	if err != nil {
		t.Fatal(err)
	}
	// With the development defaults: FRONTEND_URL and MAIL_FROM on localhost.
	w := risky.Warnings()
	for _, variable := range []string{"COOKIE_SECURE", "MAIL_DRIVER", "FRONTEND_URL", "MAIL_FROM"} {
		if !slices.ContainsFunc(w, func(s string) bool { return strings.HasPrefix(s, variable) }) {
			t.Errorf("missing the warning about %s: %q", variable, w)
		}
	}
	if len(w) != 4 {
		t.Errorf("want 4 warnings: %q", w)
	}

	loopback, err := load(envFrom(map[string]string{
		"APP_ENV":      "production",
		"DATABASE_URL": "x",
		"MAIL_DRIVER":  "smtp",
		"SMTP_HOST":    "smtp.example.com",
		"MAIL_FROM":    "My App <no-reply@example.com>",
		"FRONTEND_URL": "http://127.0.0.1:3000",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if w := loopback.Warnings(); len(w) != 1 || !strings.HasPrefix(w[0], "FRONTEND_URL") {
		t.Errorf("a loopback IP in FRONTEND_URL should warn: %q", w)
	}

	safe, err := load(envFrom(map[string]string{
		"APP_ENV":      "production",
		"DATABASE_URL": "x",
		"MAIL_DRIVER":  "smtp",
		"SMTP_HOST":    "smtp.example.com",
		"MAIL_FROM":    "no-reply@example.com",
		"FRONTEND_URL": "https://app.example.com",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if w := safe.Warnings(); len(w) != 0 {
		t.Errorf("a correct production configuration should have no warnings: %q", w)
	}

	dev, _ := load(envFrom(map[string]string{"DATABASE_URL": "x"}))
	if w := dev.Warnings(); len(w) != 0 {
		t.Errorf("there are no warnings in development: %q", w)
	}
}

func TestTrustedProxies(t *testing.T) {
	cfg, err := load(envFrom(map[string]string{"DATABASE_URL": "x", "TRUST_PROXY": "true", "TRUSTED_PROXIES": "127.0.0.1"}))
	if err != nil || fmt.Sprint(cfg.TrustedProxies) != "[127.0.0.1/32]" {
		t.Errorf("TRUSTED_PROXIES: %v (%v)", cfg.TrustedProxies, err)
	}
	for name, env := range map[string]map[string]string{
		"invalid address":             {"DATABASE_URL": "x", "TRUST_PROXY": "true", "TRUSTED_PROXIES": "caddy"},
		"proxies without TRUST_PROXY": {"DATABASE_URL": "x", "TRUSTED_PROXIES": "127.0.0.1"},
	} {
		if _, err := load(envFrom(env)); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

func TestSessionIdleTimeout(t *testing.T) {
	for value, want := range map[string]time.Duration{"": 0, "0": 0, "30m": 30 * time.Minute, "2h": 2 * time.Hour} {
		cfg, err := load(envFrom(map[string]string{"DATABASE_URL": "x", "SESSION_IDLE_TIMEOUT": value}))
		if err != nil || cfg.SessionIdleTimeout != want {
			t.Errorf("SESSION_IDLE_TIMEOUT=%q: %v (%v)", value, cfg.SessionIdleTimeout, err)
		}
	}
	// Activity is saved every 5 minutes: less than 15 would be inaccurate.
	for _, value := range []string{"5m", "-1h", "half an hour"} {
		if _, err := load(envFrom(map[string]string{"DATABASE_URL": "x", "SESSION_IDLE_TIMEOUT": value})); err == nil {
			t.Errorf("SESSION_IDLE_TIMEOUT=%q should be an error", value)
		}
	}
}
