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
		// Sin el parámetro, al menos defaultMaxConns (pgx usaría uno por núcleo si son más).
		"sin pool_max_conns":   {"postgres://u:p@localhost:5432/app?sslmode=disable", int32(max(defaultMaxConns, runtime.NumCPU()))},
		"con pool_max_conns":   {"postgres://u:p@localhost:5432/app?sslmode=disable&pool_max_conns=3", 3},
		"formato clave=valor":  {"host=localhost user=u dbname=app pool_max_conns=2", 2},
		"clave=valor sin pool": {"host=localhost user=u dbname=app", int32(max(defaultMaxConns, runtime.NumCPU()))},
	} {
		cfg, err := poolConfig(tt.url)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if cfg.MaxConns != tt.want {
			t.Errorf("%s: MaxConns = %d, se esperaba %d", name, cfg.MaxConns, tt.want)
		}
	}

	if _, err := poolConfig("postgres://u:p@localhost:5432/app?pool_max_conns=no"); err == nil {
		t.Error("se esperaba error con un pool_max_conns inválido")
	}
}
