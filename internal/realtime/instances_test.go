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

// listening crea el broker de una instancia de la API, conectado con los de las demás (Listen).
// Deja de escuchar al terminar el test.
func listening(t *testing.T, pool *pgxpool.Pool) *realtime.Broker {
	t.Helper()
	b := realtime.NewBroker()
	if err := b.Listen(t.Context(), pool, slog.New(slog.DiscardHandler)); err != nil {
		t.Fatal(err)
	}
	return b
}

// Dos instancias de la API sobre la misma base: lo publicado en una llega a las conexiones de las dos.
func TestEventsReachOtherInstances(t *testing.T) {
	pool := testutil.NewDB(t)
	brokerA, brokerB := listening(t, pool), listening(t, pool)
	a := testutil.NewServer(t, pool, testutil.WithBroker(brokerA))
	b := testutil.NewServer(t, pool, testutil.WithBroker(brokerB))
	ana, anaCookie := testutil.Login(t, pool, "ana@ejemplo.com", auth.RoleUser)
	_, luisCookie := testutil.Login(t, pool, "luis@ejemplo.com", auth.RoleUser)

	onA := testutil.OpenEvents(t, a, anaCookie, "")
	onB := testutil.OpenEvents(t, b, anaCookie, "")
	luisOnB := testutil.OpenEvents(t, b, luisCookie, "")

	brokerA.PublishToUser(t.Context(), ana.ID, realtime.Event{Type: "notification", Data: map[string]any{"id": 1234567, "message": "hola"}})
	for name, r := range map[string]*ssetest.Reader{"A": onA, "B": onB} {
		if ev := r.Next(t, 2*time.Second); ev.Type != "notification" || ev.Data != `{"id":1234567,"message":"hola"}` {
			t.Errorf("evento en la instancia %s: %+v", name, ev)
		}
	}
	// La instancia que lo publicó no lo recibe dos veces (una propia y otra por NOTIFY).
	onA.Nothing(t, 200*time.Millisecond)

	// Un evento grande no entra en un NOTIFY: viaja guardado en la base, y llega igual.
	big := strings.Repeat("ñ", 10000)
	brokerA.PublishToUser(t.Context(), ana.ID, realtime.Event{Type: "note.updated", Data: map[string]string{"body": big}})
	if ev := onB.Next(t, 2*time.Second); ev.Type != "note.updated" || ev.Data != `{"body":"`+big+`"}` {
		t.Errorf("evento grande en la otra instancia: %q (%d bytes)", ev.Type, len(ev.Data))
	}
	onA.Next(t, 2*time.Second)

	luisOnB.Nothing(t, 100*time.Millisecond)
}

// Un logout o un cierre de sesiones en una instancia corta en el acto los streams de las otras,
// sin esperar al latido.
func TestSessionsEndInAllInstances(t *testing.T) {
	pool := testutil.NewDB(t)
	brokerA, brokerB := listening(t, pool), listening(t, pool)
	apiA := testutil.NewAPI(pool, testutil.WithBroker(brokerA))
	b := testutil.NewServer(t, pool, testutil.WithBroker(brokerB))
	ana, cookie := testutil.Login(t, pool, "ana@ejemplo.com", auth.RoleUser)

	onB := testutil.OpenEvents(t, b, cookie, "")
	brokerA.DisconnectUser(t.Context(), ana.ID)
	if !onB.Closed(2 * time.Second) {
		t.Error("DisconnectUser en una instancia debería cortar los streams de la otra")
	}

	onB = testutil.OpenEvents(t, b, cookie, "")
	if w := testutil.Do(t, apiA, cookie, http.MethodPost, "/auth/logout", ""); w.Code != http.StatusNoContent {
		t.Fatalf("logout: status = %d", w.Code)
	}
	if !onB.Closed(2 * time.Second) {
		t.Error("el logout en una instancia debería cortar el stream de esa sesión en la otra")
	}
}

// Si se corta la conexión de LISTEN, el broker se reconecta y corta los streams abiertos: mientras
// no escuchaba pudo perderse algún evento, y al reconectarse el navegador pide el estado.
func TestListenReconnects(t *testing.T) {
	pool := testutil.NewDB(t)
	brokerA, brokerB := listening(t, pool), listening(t, pool)
	b := testutil.NewServer(t, pool, testutil.WithBroker(brokerB))
	ana, cookie := testutil.Login(t, pool, "ana@ejemplo.com", auth.RoleUser)
	onB := testutil.OpenEvents(t, b, cookie, "")

	// Se cae la conexión de LISTEN de las dos instancias, como si se reiniciara PostgreSQL.
	if _, err := pool.Exec(t.Context(), `SELECT pg_terminate_backend(pid) FROM pg_stat_activity
		WHERE application_name = 'realtime-listener' AND datname = current_database()`); err != nil {
		t.Fatal(err)
	}
	if !onB.Closed(5 * time.Second) {
		t.Fatal("al reconectarse, el broker debería cortar los streams abiertos")
	}

	// Ya reconectado, los eventos vuelven a llegar.
	onB = testutil.OpenEvents(t, b, cookie, "")
	brokerA.PublishToUser(t.Context(), ana.ID, realtime.Event{Type: "notification"})
	if ev := onB.Next(t, 2*time.Second); ev.Type != "notification" {
		t.Errorf("después de reconectarse: %+v", ev)
	}
}
