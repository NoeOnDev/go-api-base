package db

import (
	"runtime"
	"testing"
)

func TestPoolConfigMaxConns(t *testing.T) {
	for name, tt := range map[string]struct {
		url  string
		want int32
	}{
		// Without the parameter, at least defaultMaxConns (pgx would use one per core if there are more).
		"without pool_max_conns": {"postgres://u:p@localhost:5432/app?sslmode=disable", int32(max(defaultMaxConns, runtime.NumCPU()))},
		"with pool_max_conns":    {"postgres://u:p@localhost:5432/app?sslmode=disable&pool_max_conns=3", 3},
		"key=value format":       {"host=localhost user=u dbname=app pool_max_conns=2", 2},
		"key=value without pool": {"host=localhost user=u dbname=app", int32(max(defaultMaxConns, runtime.NumCPU()))},
	} {
		cfg, err := poolConfig(tt.url)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if cfg.MaxConns != tt.want {
			t.Errorf("%s: MaxConns = %d, want %d", name, cfg.MaxConns, tt.want)
		}
	}

	if _, err := poolConfig("postgres://u:p@localhost:5432/app?pool_max_conns=no"); err == nil {
		t.Error("want an error with an invalid pool_max_conns")
	}
}

func TestPoolConfigStatementTimeout(t *testing.T) {
	cfg, err := poolConfig("postgres://u:p@localhost:5432/app")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AfterConnect == nil {
		t.Error("without statement_timeout in the URL, each connection must set the default one")
	}

	for _, url := range []string{
		"postgres://u:p@localhost:5432/app?statement_timeout=30000",
		"host=localhost user=u dbname=app statement_timeout=0",
	} {
		cfg, err := poolConfig(url)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.AfterConnect != nil {
			t.Errorf("%s: the URL's statement_timeout must be kept", url)
		}
	}
}
