package jobs

import (
	"fmt"
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

	// Notificaciones: leída hace 100 días (se borra), leída hace 10 días y sin leer de hace un año.
	for _, n := range []struct {
		readAt  string
		created string
	}{{"now() - interval '100 days'", "now() - interval '100 days'"}, {"now() - interval '10 days'", "now()"}, {"NULL", "now() - interval '365 days'"}} {
		sql := fmt.Sprintf("INSERT INTO notifications (user_id, kind, read_at, created_at) VALUES ($1, 'x', %s, %s)", n.readAt, n.created)
		if _, err := pool.Exec(t.Context(), sql, u.ID); err != nil {
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
	if n := count("notifications"); n != 2 {
		t.Errorf("quedaron %d notificaciones, se esperaban la leída reciente y la no leída", n)
	}
}

func TestCleanupDeletesIdleSessions(t *testing.T) {
	pool := testutil.NewDB(t)
	u, _ := testutil.Login(t, pool, "ana@ejemplo.com", auth.RoleUser) // activa ahora
	if _, err := pool.Exec(t.Context(), `INSERT INTO sessions (token_hash, user_id, expires_at, last_seen_at)
		VALUES ('quieta', $1, now() + interval '1 day', now() - interval '2 hours')`, u.ID); err != nil {
		t.Fatal(err)
	}

	for _, tt := range []struct {
		idle time.Duration
		want int
	}{{0, 2}, {time.Hour, 1}} { // sin vencimiento no borra la quieta; con 1 hora, sí
		worker := rivertest.NewWorker(t, riverpgxv5.New(pool), &river.Config{},
			&cleanupWorker{q: store.New(pool), logger: slog.New(slog.DiscardHandler), idle: tt.idle})
		tx, err := pool.Begin(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := worker.Work(t.Context(), t, tx, CleanupArgs{}, nil); err != nil {
			t.Fatal(err)
		}
		_ = tx.Rollback(t.Context())

		var n int
		if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM sessions").Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != tt.want {
			t.Errorf("idle %v: quedaron %d sesiones, se esperaban %d", tt.idle, n, tt.want)
		}
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

// El apagado detiene River al mismo tiempo que el servidor HTTP (ver cmd/api/main.go): una
// petición que todavía está terminando tiene que poder encolar, y el trabajo queda para después.
func TestInsertAfterStop(t *testing.T) {
	pool := testutil.NewDB(t)
	client, err := New(pool, slog.New(slog.DiscardHandler), Config{MaxWorkers: 1}, Deps{Queries: store.New(pool), Logger: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := client.Stop(t.Context()); err != nil {
		t.Fatal(err)
	}

	res, err := client.Insert(t.Context(), CleanupArgs{}, &river.InsertOpts{UniqueOpts: river.UniqueOpts{}})
	if err != nil {
		t.Fatalf("encolar después de Stop: %v", err)
	}
	var state string
	if err := pool.QueryRow(t.Context(), "SELECT state FROM river_job WHERE id = $1", res.Job.ID).Scan(&state); err != nil || state != "available" {
		t.Errorf("el trabajo debería quedar pendiente: %q (%v)", state, err)
	}
}
