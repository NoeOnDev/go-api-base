// Package testutil reúne ayudas para los tests de integración.
package testutil

import (
	"fmt"
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/NoeOnDev/go-api-base/internal/db"
)

// NewDB conecta a la base de pruebas (TEST_DATABASE_URL), aplica las migraciones
// y vacía las tablas indicadas para que cada test empiece desde cero.
//
// Si TEST_DATABASE_URL no está definida, el test se saltea en lugar de fallar:
// así los tests unitarios siguen corriendo en una máquina sin base de datos.
//
// Cada paquete debe vaciar solo sus propias tablas, porque go test corre
// los paquetes en paralelo contra la misma base.
func NewDB(t *testing.T, tables ...string) *pgxpool.Pool {
	t.Helper()

	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL no está definida: se saltea el test de integración")
	}

	ctx := t.Context()

	pool, err := db.Open(ctx, url)
	if err != nil {
		t.Fatalf("conectar a la base de pruebas: %v", err)
	}
	t.Cleanup(pool.Close)

	if err := db.Migrate(ctx, pool, slog.New(slog.DiscardHandler)); err != nil {
		t.Fatalf("migrar la base de pruebas: %v", err)
	}

	if len(tables) > 0 {
		names := make([]string, len(tables))
		for i, table := range tables {
			names[i] = pgx.Identifier{table}.Sanitize()
		}
		sql := fmt.Sprintf("TRUNCATE %s RESTART IDENTITY CASCADE", strings.Join(names, ", "))
		if _, err := pool.Exec(ctx, sql); err != nil {
			t.Fatalf("vaciar tablas: %v", err)
		}
	}

	return pool
}
