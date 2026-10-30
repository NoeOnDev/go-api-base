package db_test

import (
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/NoeOnDev/go-api-base/internal/testutil"
)

func TestStatementTimeout(t *testing.T) {
	pool := testutil.NewDB(t)

	var timeout string
	if err := pool.QueryRow(t.Context(), "SHOW statement_timeout").Scan(&timeout); err != nil {
		t.Fatal(err)
	}
	if timeout != "10s" {
		t.Errorf("statement_timeout = %q, want 10s", timeout)
	}

	// A transaction can change it with SET LOCAL; a statement past the limit is cancelled.
	err := pgx.BeginFunc(t.Context(), pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(t.Context(), "SET LOCAL statement_timeout = '50ms'"); err != nil {
			t.Fatal(err)
		}
		_, err := tx.Exec(t.Context(), "SELECT pg_sleep(1)")
		return err
	})
	if err == nil {
		t.Error("want an error from a statement past the limit")
	}
}
