package realtime_test

import (
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/NoeOnDev/go-api-base/internal/auth"
	"github.com/NoeOnDev/go-api-base/internal/realtime"
	"github.com/NoeOnDev/go-api-base/internal/realtime/ssetest"
	"github.com/NoeOnDev/go-api-base/internal/testutil"
)

// listening creates the broker of an API instance, connected with those of the others (Listen).
// It stops listening when the test ends.
func listening(t *testing.T, pool *pgxpool.Pool) *realtime.Broker {
	t.Helper()
	b := realtime.NewBroker()
	if err := b.Listen(t.Context(), pool, slog.New(slog.DiscardHandler)); err != nil {
		t.Fatal(err)
	}
	return b
}

// Two API instances on the same database: what is published on one reaches the connections of both.
func TestEventsReachOtherInstances(t *testing.T) {
	pool := testutil.NewDB(t)
	brokerA, brokerB := listening(t, pool), listening(t, pool)
	a := testutil.NewServer(t, pool, testutil.WithBroker(brokerA))
	b := testutil.NewServer(t, pool, testutil.WithBroker(brokerB))
	alice, aliceCookie := testutil.Login(t, pool, "alice@example.com", auth.RoleUser)
	_, bobCookie := testutil.Login(t, pool, "bob@example.com", auth.RoleUser)

	onA := testutil.OpenEvents(t, a, aliceCookie, "")
	onB := testutil.OpenEvents(t, b, aliceCookie, "")
	bobOnB := testutil.OpenEvents(t, b, bobCookie, "")

	brokerA.PublishToUser(t.Context(), alice.ID, realtime.Event{Type: "notification", Data: map[string]any{"id": 1234567, "message": "hello"}})
	for name, r := range map[string]*ssetest.Reader{"A": onA, "B": onB} {
		if ev := r.Next(t, 2*time.Second); ev.Type != "notification" || ev.Data != `{"id":1234567,"message":"hello"}` {
			t.Errorf("event on instance %s: %+v", name, ev)
		}
	}
	// The instance that published it does not get it twice (once its own and once through NOTIFY).
	onA.Nothing(t, 200*time.Millisecond)

	// A large event does not fit in a NOTIFY: it travels stored in the database, and arrives the same.
	big := strings.Repeat("ñ", 10000)
	brokerA.PublishToUser(t.Context(), alice.ID, realtime.Event{Type: "note.updated", Data: map[string]string{"body": big}})
	if ev := onB.Next(t, 2*time.Second); ev.Type != "note.updated" || ev.Data != `{"body":"`+big+`"}` {
		t.Errorf("large event on the other instance: %q (%d bytes)", ev.Type, len(ev.Data))
	}
	onA.Next(t, 2*time.Second)

	bobOnB.Nothing(t, 100*time.Millisecond)
}

// A logout or closing sessions on one instance cuts the streams of the others right away,
// without waiting for the heartbeat.
func TestSessionsEndInAllInstances(t *testing.T) {
	pool := testutil.NewDB(t)
	brokerA, brokerB := listening(t, pool), listening(t, pool)
	apiA := testutil.NewAPI(pool, testutil.WithBroker(brokerA))
	b := testutil.NewServer(t, pool, testutil.WithBroker(brokerB))
	alice, cookie := testutil.Login(t, pool, "alice@example.com", auth.RoleUser)

	onB := testutil.OpenEvents(t, b, cookie, "")
	brokerA.DisconnectUser(t.Context(), alice.ID)
	if !onB.Closed(2 * time.Second) {
		t.Error("DisconnectUser on one instance should cut the streams of the other")
	}

	onB = testutil.OpenEvents(t, b, cookie, "")
	if w := testutil.Do(t, apiA, cookie, http.MethodPost, "/auth/logout", ""); w.Code != http.StatusNoContent {
		t.Fatalf("logout: status = %d", w.Code)
	}
	if !onB.Closed(2 * time.Second) {
		t.Error("the logout on one instance should cut the stream of that session on the other")
	}
}

// If the LISTEN connection drops, the broker reconnects and cuts the open streams: while it was
// not listening some event could have been lost, and on reconnecting the browser asks for the
// state.
func TestListenReconnects(t *testing.T) {
	pool := testutil.NewDB(t)
	brokerA, brokerB := listening(t, pool), listening(t, pool)
	b := testutil.NewServer(t, pool, testutil.WithBroker(brokerB))
	alice, cookie := testutil.Login(t, pool, "alice@example.com", auth.RoleUser)
	onB := testutil.OpenEvents(t, b, cookie, "")

	// The LISTEN connection of both instances drops, as if PostgreSQL restarted.
	if _, err := pool.Exec(t.Context(), `SELECT pg_terminate_backend(pid) FROM pg_stat_activity
		WHERE application_name = 'realtime-listener' AND datname = current_database()`); err != nil {
		t.Fatal(err)
	}
	if !onB.Closed(5 * time.Second) {
		t.Fatal("on reconnecting, the broker should cut the open streams")
	}

	// Once reconnected, the events arrive again.
	onB = testutil.OpenEvents(t, b, cookie, "")
	brokerA.PublishToUser(t.Context(), alice.ID, realtime.Event{Type: "notification"})
	if ev := onB.Next(t, 2*time.Second); ev.Type != "notification" {
		t.Errorf("after reconnecting: %+v", ev)
	}
}
