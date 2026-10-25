package jobs

import (
	"log/slog"
	"testing"
	"time"

	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivertest"

	"github.com/NoeOnDev/go-api-base/internal/auth"
	"github.com/NoeOnDev/go-api-base/internal/store"
	"github.com/NoeOnDev/go-api-base/internal/testutil"
)

func TestCleanupDeletesOnlyExpired(t *testing.T) {
	pool := testutil.NewDB(t)
	q := store.New(pool)
	u, _ := testutil.Login(t, pool, "ana@ejemplo.com", auth.RoleUser) // una sesión vigente

	// Una sesión y un token vencidos, y un token vigente.
	past, future := time.Now().Add(-time.Hour), time.Now().Add(time.Hour)
	if err := q.CreateSession(t.Context(), store.CreateSessionParams{TokenHash: []byte("sesión vencida"), UserID: u.ID, ExpiresAt: past}); err != nil {
		t.Fatal(err)
	}
	for _, tk := range []struct {
		hash    string
		expires time.Time
	}{{"token vencido", past}, {"token vigente", future}} {
		if err := q.CreateUserToken(t.Context(), store.CreateUserTokenParams{TokenHash: []byte(tk.hash), UserID: u.ID, Purpose: "password_reset", ExpiresAt: tk.expires}); err != nil {
			t.Fatal(err)
		}
	}

	driver := riverpgxv5.New(pool)
	worker := rivertest.NewWorker(t, driver, &river.Config{}, &cleanupWorker{q: q, logger: slog.New(slog.DiscardHandler)})
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(t.Context())
	if _, err := worker.Work(t.Context(), t, tx, CleanupArgs{}, nil); err != nil {
		t.Fatal(err)
	}

	count := func(table string) int {
		var n int
		if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM "+table).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if n := count("sessions"); n != 1 {
		t.Errorf("quedaron %d sesiones, se esperaba solo la vigente", n)
	}
	if n := count("user_tokens"); n != 1 {
		t.Errorf("quedaron %d tokens, se esperaba solo el vigente", n)
	}
}

func TestNewRegistersWorkersAndPeriodicJobs(t *testing.T) {
	pool := testutil.NewDB(t)
	client, err := New(pool, slog.New(slog.DiscardHandler), Config{MaxWorkers: 2}, Deps{Queries: store.New(pool), Logger: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatal(err)
	}

	// La limpieza se puede encolar, y por ser única por hora, la segunda no se duplica.
	first, err := client.Insert(t.Context(), CleanupArgs{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := client.Insert(t.Context(), CleanupArgs{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !second.UniqueSkippedAsDuplicate || second.Job.ID != first.Job.ID {
		t.Errorf("la segunda limpieza de la hora debería saltearse: %+v", second)
	}
}
