package config

import (
	"log/slog"
	"testing"
	"time"
)

func envFrom(m map[string]string) func(string) string {
	return func(key string) string { return m[key] }
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
}

func TestLoadCustomValues(t *testing.T) {
	cfg, err := load(envFrom(map[string]string{
		"APP_ENV":          "production",
		"HTTP_ADDR":        "127.0.0.1:9000",
		"DATABASE_URL":     "postgres://localhost/db",
		"LOG_LEVEL":        "debug",
		"SHUTDOWN_TIMEOUT": "30s",
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
