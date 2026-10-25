package realtime

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/NoeOnDev/go-api-base/internal/auth"
	"github.com/NoeOnDev/go-api-base/internal/realtime/ssetest"
)

func TestStreamFormat(t *testing.T) {
	w := httptest.NewRecorder()
	s, err := Open(w, httptest.NewRequest(http.MethodGet, "/events", nil))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Send(Event{ID: "7", Type: "note.updated", Data: map[string]any{"id": 7, "title": "línea 1\nlínea 2"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.Comment("ping"); err != nil {
		t.Fatal(err)
	}

	// El salto de línea del dato viaja escapado dentro del JSON: no corta el evento.
	want := "id: 7\nevent: note.updated\ndata: {\"id\":7,\"title\":\"línea 1\\nlínea 2\"}\n\n: ping\n\n"
	if got := w.Body.String(); got != want {
		t.Errorf("formato SSE:\n%q\nse esperaba:\n%q", got, want)
	}
	for k, v := range map[string]string{"Content-Type": "text/event-stream", "Cache-Control": "no-store", "X-Accel-Buffering": "no"} {
		if got := w.Header().Get(k); got != v {
			t.Errorf("%s = %q", k, got)
		}
	}

	// Un salto de línea en el tipo inyectaría otro campo: se rechaza.
	if err := s.Send(Event{Type: "x\ndata: inyectado"}); err == nil {
		t.Error("se esperaba un error por el salto de línea en el tipo")
	}
}

func TestBrokerLimits(t *testing.T) {
	b := NewBroker()
	b.maxPerUser, b.maxTotal = 2, 3

	a1, _ := b.subscribe(1, nil)
	if _, err := b.subscribe(1, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := b.subscribe(1, nil); !errors.Is(err, ErrTooManyConnections) {
		t.Errorf("tercera conexión del usuario: %v", err)
	}
	if _, err := b.subscribe(2, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := b.subscribe(3, nil); !errors.Is(err, ErrTooManyConnections) {
		t.Errorf("superado el total: %v", err)
	}

	// Al cerrar una conexión se libera su lugar.
	b.unsubscribe(a1)
	if _, err := b.subscribe(3, nil); err != nil {
		t.Errorf("después de liberar un lugar: %v", err)
	}
}

func TestBrokerClosesSlowSubscriber(t *testing.T) {
	b := NewBroker()
	sub, _ := b.subscribe(1, nil)

	// Nadie lee: al llenarse el buffer, la conexión se cierra en lugar de frenar a los demás.
	for range subscriptionBuffer + 1 {
		b.PublishToUser(t.Context(), 1, Event{Type: "x"})
	}
	select {
	case <-sub.closed:
	default:
		t.Error("la conexión lenta debería haberse cerrado")
	}
}

func TestBrokerClose(t *testing.T) {
	b := NewBroker()
	sub, _ := b.subscribe(1, nil)
	b.Close()
	b.Close() // cerrar dos veces no falla

	select {
	case <-sub.closed:
	default:
		t.Error("Close debería cerrar las conexiones abiertas")
	}
	if _, err := b.subscribe(1, nil); !errors.Is(err, ErrClosed) {
		t.Errorf("después de Close: %v", err)
	}
}

// testServer levanta el handler en un servidor HTTP real, con el WriteTimeout indicado.
// El usuario se elige con el encabezado X-Test-User (en la app lo pone RequireUser).
func testServer(t *testing.T, b *Broker, writeTimeout time.Duration) *httptest.Server {
	t.Helper()
	h := b.Handler()
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, _ := strconv.ParseInt(r.Header.Get("X-Test-User"), 10, 64)
		h.ServeHTTP(w, r.WithContext(auth.WithUser(r.Context(), auth.User{ID: id})))
	}))
	srv.Config.WriteTimeout = writeTimeout
	srv.Config.RegisterOnShutdown(b.Close)
	srv.Start()
	t.Cleanup(srv.Close)
	return srv
}

func connect(t *testing.T, srv *httptest.Server, userID int64, query string) (*http.Response, *ssetest.Reader) {
	t.Helper()
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL+"/events"+query, nil)
	req.Header.Set("X-Test-User", strconv.FormatInt(userID, 10))
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp, ssetest.NewReader(resp.Body)
}

func TestEventsHandler(t *testing.T) {
	old := heartbeatInterval
	heartbeatInterval = 50 * time.Millisecond
	t.Cleanup(func() { heartbeatInterval = old })

	b := NewBroker()
	b.Authorize("project", func(_ context.Context, userID int64, id string) (bool, error) {
		return userID == 1 && id == "42", nil // solo el usuario 1 participa del proyecto 42
	})
	// Un WriteTimeout corto: sin quitarlo, el servidor cortaría el stream enseguida.
	srv := testServer(t, b, 200*time.Millisecond)

	resp, ana := connect(t, srv, 1, "?topics=project:42")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if ev := ana.Next(t, time.Second); ev.Type != "ready" || ev.Data != `{"topics":["project:42"]}` {
		t.Fatalf("primer evento: %+v", ev)
	}
	_, luis := connect(t, srv, 2, "")
	luis.Next(t, time.Second) // ready

	// Pasado el WriteTimeout, el stream sigue vivo, con latidos.
	time.Sleep(300 * time.Millisecond)
	if c := ana.NextComment(t, time.Second); c != "ping" {
		t.Errorf("latido = %q", c)
	}

	// Cada uno recibe lo de su canal y de sus temas, y nada más.
	b.PublishToUser(t.Context(), 1, Event{Type: "notification", Data: "hola Ana"})
	b.Publish(t.Context(), "project:42", Event{Type: "task.moved", Data: 5})
	if ev := ana.Next(t, time.Second); ev.Type != "notification" || ev.Data != `"hola Ana"` {
		t.Errorf("evento del usuario: %+v", ev)
	}
	if ev := ana.Next(t, time.Second); ev.Type != "task.moved" || ev.Data != "5" {
		t.Errorf("evento del tema: %+v", ev)
	}
	luis.Nothing(t, 100*time.Millisecond)
}

func TestEventsHandlerRejects(t *testing.T) {
	b := NewBroker()
	b.maxPerUser = 1
	b.Authorize("project", func(context.Context, int64, string) (bool, error) { return false, nil })
	srv := testServer(t, b, 0)

	for name, query := range map[string]string{
		"tema no autorizado":     "?topics=project:42",
		"tipo de tema sin dueño": "?topics=invoice:1",
		"canal de otro usuario":  "?topics=user:2",
		"tema sin id":            "?topics=project",
	} {
		if resp, _ := connect(t, srv, 1, query); resp.StatusCode != http.StatusForbidden {
			t.Errorf("%s: status = %d, se esperaba 403", name, resp.StatusCode)
		}
	}

	connect(t, srv, 1, "")
	resp, _ := connect(t, srv, 1, "")
	if resp.StatusCode != http.StatusTooManyRequests || resp.Header.Get("Retry-After") == "" {
		t.Errorf("segunda conexión con límite 1: status = %d", resp.StatusCode)
	}
}

func TestShutdownClosesStreams(t *testing.T) {
	b := NewBroker()
	srv := testServer(t, b, 0)
	_, r := connect(t, srv, 1, "")
	r.Next(t, time.Second) // ready

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	start := time.Now()
	if err := srv.Config.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	// Sin RegisterOnShutdown(b.Close), Shutdown esperaría todo el timeout.
	if d := time.Since(start); d > time.Second {
		t.Errorf("el apagado tardó %v", d)
	}
	if !r.Closed(time.Second) {
		t.Error("el stream debería haberse cerrado al apagar")
	}
}
