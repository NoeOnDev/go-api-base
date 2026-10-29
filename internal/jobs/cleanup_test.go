package jobs

import (
	"fmt"
	"log/slog"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivertest"

	"github.com/NoeOnDev/go-api-base/internal/auth"
	"github.com/NoeOnDev/go-api-base/internal/store"
	"github.com/NoeOnDev/go-api-base/internal/testutil"
)

// runCleanup runs the cleanup job once. idle is the idle expiry of the sessions (0: off).
func runCleanup(t *testing.T, pool *pgxpool.Pool, idle time.Duration) {
	t.Helper()
	worker := rivertest.NewWorker(t, riverpgxv5.New(pool), &river.Config{},
		&cleanupWorker{q: store.New(pool), logger: slog.New(slog.DiscardHandler), idle: idle})
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(t.Context())
	if _, err := worker.Work(t.Context(), t, tx, CleanupArgs{}, nil); err != nil {
		t.Fatal(err)
	}
}

// count counts the rows of the table.
func count(t *testing.T, pool *pgxpool.Pool, table string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM "+table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestCleanupDeletesOnlyExpired(t *testing.T) {
	pool := testutil.NewDB(t)
	q := store.New(pool)
	u, _ := testutil.Login(t, pool, "alice@example.com", auth.RoleUser) // a valid session

	// An expired session and token, and a valid token.
	past, future := time.Now().Add(-time.Hour), time.Now().Add(time.Hour)
	if err := q.CreateSession(t.Context(), store.CreateSessionParams{TokenHash: []byte("expired session"), UserID: u.ID, ExpiresAt: past}); err != nil {
		t.Fatal(err)
	}
	for _, tk := range []struct {
		hash    string
		expires time.Time
	}{{"expired token", past}, {"valid token", future}} {
		if err := q.CreateUserToken(t.Context(), store.CreateUserTokenParams{TokenHash: []byte(tk.hash), UserID: u.ID, Purpose: "password_reset", ExpiresAt: tk.expires}); err != nil {
			t.Fatal(err)
		}
	}

	// Rate limits: one already refilled (deleted) and another that is not yet.
	if _, err := pool.Exec(t.Context(), `INSERT INTO rate_limits (key, tat)
		VALUES ('recargado', now() - interval '1 minute'), ('en uso', now() + interval '1 minute')`); err != nil {
		t.Fatal(err)
	}

	runCleanup(t, pool, 0)

	if n := count(t, pool, "sessions"); n != 1 {
		t.Errorf("%d sessions left, want only the valid one", n)
	}
	if n := count(t, pool, "user_tokens"); n != 1 {
		t.Errorf("%d tokens left, want only the valid one", n)
	}
	if n := count(t, pool, "rate_limits"); n != 1 {
		t.Errorf("%d rate limits left, want only the one that did not refill", n)
	}
}

func TestCleanupDeletesOldNotifications(t *testing.T) {
	pool := testutil.NewDB(t)
	u, _ := testutil.Login(t, pool, "alice@example.com", auth.RoleUser)

	// Read 100 days ago (deleted), read 10 days ago and unread from a year ago.
	for _, n := range []struct {
		readAt  string
		created string
	}{{"now() - interval '100 days'", "now() - interval '100 days'"}, {"now() - interval '10 days'", "now()"}, {"NULL", "now() - interval '365 days'"}} {
		sql := fmt.Sprintf("INSERT INTO notifications (user_id, kind, read_at, created_at) VALUES ($1, 'x', %s, %s)", n.readAt, n.created)
		if _, err := pool.Exec(t.Context(), sql, u.ID); err != nil {
			t.Fatal(err)
		}
	}

	runCleanup(t, pool, 0)

	if n := count(t, pool, "notifications"); n != 2 {
		t.Errorf("%d notifications left, want the recently read one and the unread one", n)
	}
}

func TestCleanupDeletesOldRealtimeMessages(t *testing.T) {
	pool := testutil.NewDB(t)

	// Large real-time messages: one from an hour ago (deleted) and another just stored.
	if _, err := pool.Exec(t.Context(), `INSERT INTO realtime_messages (payload, created_at)
		VALUES ('{}', now() - interval '1 hour'), ('{}', now())`); err != nil {
		t.Fatal(err)
	}

	runCleanup(t, pool, 0)

	if n := count(t, pool, "realtime_messages"); n != 1 {
		t.Errorf("%d real-time messages left, want only the recent one", n)
	}
}

func TestCleanupDeletesIdleSessions(t *testing.T) {
	pool := testutil.NewDB(t)
	u, _ := testutil.Login(t, pool, "alice@example.com", auth.RoleUser) // active now
	if _, err := pool.Exec(t.Context(), `INSERT INTO sessions (token_hash, user_id, expires_at, last_seen_at)
		VALUES ('idle', $1, now() + interval '1 day', now() - interval '2 hours')`, u.ID); err != nil {
		t.Fatal(err)
	}

	for _, tt := range []struct {
		idle time.Duration
		want int
	}{{0, 2}, {time.Hour, 1}} { // without an expiry it does not delete the idle one; with 1 hour, it does
		runCleanup(t, pool, tt.idle)
		if n := count(t, pool, "sessions"); n != tt.want {
			t.Errorf("idle %v: %d sessions left, want %d", tt.idle, n, tt.want)
		}
	}
}

func TestNewRegistersWorkersAndPeriodicJobs(t *testing.T) {
	pool := testutil.NewDB(t)
	client, err := New(pool, slog.New(slog.DiscardHandler), Config{MaxWorkers: 2}, Deps{Queries: store.New(pool), Logger: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatal(err)
	}

	// The cleanup can be queued, and since it is unique per hour, the second one is not duplicated.
	first, err := client.Insert(t.Context(), CleanupArgs{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := client.Insert(t.Context(), CleanupArgs{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !second.UniqueSkippedAsDuplicate || second.Job.ID != first.Job.ID {
		t.Errorf("the second cleanup of the hour should be skipped: %+v", second)
	}
}

// The shutdown stops River at the same time as the HTTP server (see cmd/api/main.go): a request
// that is still finishing must be able to queue, and the job stays for later.
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
		t.Fatalf("queue after Stop: %v", err)
	}
	var state string
	if err := pool.QueryRow(t.Context(), "SELECT state FROM river_job WHERE id = $1", res.Job.ID).Scan(&state); err != nil || state != "available" {
		t.Errorf("the job should stay pending: %q (%v)", state, err)
	}
}
