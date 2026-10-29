package realtime

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
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

	a1, _ := b.subscribe(1, "s1", nil)
	if _, err := b.subscribe(1, "s1", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := b.subscribe(1, "s1", nil); !errors.Is(err, ErrTooManyConnections) {
		t.Errorf("tercera conexión del usuario: %v", err)
	}
	if _, err := b.subscribe(2, "s2", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := b.subscribe(3, "s3", nil); !errors.Is(err, ErrTooManyConnections) {
		t.Errorf("superado el total: %v", err)
	}

	// Al cerrar una conexión se libera su lugar.
	b.unsubscribe(a1)
	if _, err := b.subscribe(3, "s3", nil); err != nil {
		t.Errorf("después de liberar un lugar: %v", err)
	}
}

func TestBrokerClosesSlowSubscriber(t *testing.T) {
	b := NewBroker()
	sub, _ := b.subscribe(1, "s1", nil)

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
	sub, _ := b.subscribe(1, "s1", nil)
	b.Close()
	b.Close() // cerrar dos veces no falla

	select {
	case <-sub.closed:
	default:
		t.Error("Close debería cerrar las conexiones abiertas")
	}
	if _, err := b.subscribe(1, "s1", nil); !errors.Is(err, ErrClosed) {
		t.Errorf("después de Close: %v", err)
	}
}

// fakeSessions reemplaza a auth: la sesión es el encabezado X-Test-Session, y las que estén en
// ended se consideran cerradas.
type fakeSessions struct {
	mu    sync.Mutex
	ended map[string]bool
}

func (f *fakeSessions) SessionKey(r *http.Request) string { return r.Header.Get("X-Test-Session") }

func (f *fakeSessions) CheckSession(r *http.Request) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ended[f.SessionKey(r)] {
		return errors.New("sesión cerrada")
	}
	return nil
}

func (f *fakeSessions) end(key string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ended[key] = true
}

// testServer levanta el handler en un servidor HTTP real, con el WriteTimeout indicado.
// El usuario se elige con el encabezado X-Test-User (en la app lo pone RequireUser), y la
// sesión con X-Test-Session (por defecto, "s<usuario>").
func testServer(t *testing.T, b *Broker, writeTimeout time.Duration, sessions ...*fakeSessions) *httptest.Server {
	t.Helper()
	fs := &fakeSessions{ended: map[string]bool{}}
	if len(sessions) > 0 {
		fs = sessions[0]
	}
	h := b.Handler(fs)
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

func connect(t *testing.T, srv *httptest.Server, userID int64, query string, session ...string) (*http.Response, *ssetest.Reader) {
	t.Helper()
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL+"/events"+query, nil)
	req.Header.Set("X-Test-User", strconv.FormatInt(userID, 10))
	key := "s" + strconv.FormatInt(userID, 10)
	if len(session) > 0 {
		key = session[0]
	}
	req.Header.Set("X-Test-Session", key)
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

	resp, alice := connect(t, srv, 1, "?topics=project:42")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if ev := alice.Next(t, time.Second); ev.Type != "ready" || ev.Data != `{"topics":["project:42"],"rejected":[]}` {
		t.Fatalf("primer evento: %+v", ev)
	}
	_, bob := connect(t, srv, 2, "")
	if ev := bob.Next(t, time.Second); ev.Data != `{"topics":[],"rejected":[]}` {
		t.Errorf("ready sin temas: %+v", ev)
	}

	// Pasado el WriteTimeout, el stream sigue vivo, con latidos.
	time.Sleep(300 * time.Millisecond)
	if c := alice.NextComment(t, time.Second); c != "ping" {
		t.Errorf("latido = %q", c)
	}

	// Cada uno recibe lo de su canal y de sus temas, y nada más.
	b.PublishToUser(t.Context(), 1, Event{Type: "notification", Data: "hola Alice"})
	b.Publish(t.Context(), "project:42", Event{Type: "task.moved", Data: 5})
	if ev := alice.Next(t, time.Second); ev.Type != "notification" || ev.Data != `"hola Alice"` {
		t.Errorf("evento del usuario: %+v", ev)
	}
	if ev := alice.Next(t, time.Second); ev.Type != "task.moved" || ev.Data != "5" {
		t.Errorf("evento del tema: %+v", ev)
	}
	bob.Nothing(t, 100*time.Millisecond)
}

func TestEventsHandlerRejectedTopics(t *testing.T) {
	b := NewBroker()
	b.Authorize("project", func(_ context.Context, _ int64, id string) (bool, error) { return id == "1", nil })
	srv := testServer(t, b, 0)

	// Un tema sin permiso no corta la conexión: se informa en ready, y el resto sigue andando.
	resp, r := connect(t, srv, 1, "?topics=project:1,project:42,invoice:1,user:2,project")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	want := `{"topics":["project:1"],"rejected":["project:42","invoice:1","user:2","project"]}`
	if ev := r.Next(t, time.Second); ev.Type != "ready" || ev.Data != want {
		t.Fatalf("ready = %+v, se esperaba %s", ev, want)
	}

	// Recibe su canal y el tema permitido; nada de los rechazados.
	b.Publish(t.Context(), "project:42", Event{Type: "ajeno"})
	b.Publish(t.Context(), "user:2", Event{Type: "ajeno"})
	b.PublishToUser(t.Context(), 1, Event{Type: "notification"})
	b.Publish(t.Context(), "project:1", Event{Type: "task.moved"})
	if ev := r.Next(t, time.Second); ev.Type != "notification" {
		t.Errorf("evento del usuario: %+v", ev)
	}
	if ev := r.Next(t, time.Second); ev.Type != "task.moved" {
		t.Errorf("evento del tema permitido: %+v", ev)
	}
	r.Nothing(t, 100*time.Millisecond)
}

func TestEventsHandlerConnectionLimit(t *testing.T) {
	b := NewBroker()
	b.maxPerUser = 1
	srv := testServer(t, b, 0)

	connect(t, srv, 1, "")
	resp, _ := connect(t, srv, 1, "")
	if resp.StatusCode != http.StatusTooManyRequests || resp.Header.Get("Retry-After") == "" {
		t.Errorf("segunda conexión con límite 1: status = %d", resp.StatusCode)
	}
}

func TestStreamLifetimeVaries(t *testing.T) {
	seen := map[time.Duration]bool{}
	for range 100 {
		d := streamLifetime()
		if d < 13*time.Minute || d > 17*time.Minute {
			t.Fatalf("vida del stream fuera de 13 a 17 minutos: %v", d)
		}
		seen[d] = true
	}
	if len(seen) < 50 {
		t.Errorf("la vida del stream casi no varía: %d valores distintos en 100", len(seen))
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

func TestStreamsEndWithTheirSession(t *testing.T) {
	b := NewBroker()
	srv := testServer(t, b, 0)

	// Alice tiene dos sesiones (dos dispositivos), y Bob una.
	_, phone := connect(t, srv, 1, "", "telefono")
	_, laptop := connect(t, srv, 1, "", "laptop")
	_, bob := connect(t, srv, 2, "")
	for _, r := range []*ssetest.Reader{phone, laptop, bob} {
		r.Next(t, time.Second) // ready
	}

	// Logout en el teléfono: se corta solo ese stream.
	b.SessionEnded(t.Context(), "telefono")
	if !phone.Closed(time.Second) {
		t.Error("el stream del teléfono debería cortarse con el logout")
	}
	if laptop.Closed(100 * time.Millisecond) {
		t.Error("el stream de la laptop no debería cortarse")
	}

	// Alice cambia la contraseña desde la laptop: se cortan las demás sesiones, no la suya.
	_, other := connect(t, srv, 1, "", "sesion-robada")
	other.Next(t, time.Second)
	b.UserSessionsEnded(t.Context(), 1, "laptop")
	if !other.Closed(time.Second) {
		t.Error("el stream de la otra sesión debería cortarse")
	}
	if laptop.Closed(100 * time.Millisecond) {
		t.Error("el stream de la sesión que sigue no debería cortarse")
	}

	// Recuperar la contraseña cierra todas.
	b.UserSessionsEnded(t.Context(), 1, "")
	if !laptop.Closed(time.Second) {
		t.Error("recuperar la contraseña debería cortar todos los streams del usuario")
	}
	if bob.Closed(100 * time.Millisecond) {
		t.Error("los streams de otro usuario no se tocan")
	}
}

func TestHeartbeatChecksSession(t *testing.T) {
	old := heartbeatInterval
	heartbeatInterval = 50 * time.Millisecond
	t.Cleanup(func() { heartbeatInterval = old })

	b := NewBroker()
	sessions := &fakeSessions{ended: map[string]bool{}}
	srv := testServer(t, b, 0, sessions)
	_, r := connect(t, srv, 1, "")
	r.Next(t, time.Second)

	// La sesión se cerró sin avisar al broker (por ejemplo, venció o se borró con SQL):
	// el próximo latido lo detecta y corta el stream.
	sessions.end("s1")
	if !r.Closed(time.Second) {
		t.Error("el stream debería cortarse en el latido siguiente")
	}
}

func TestStreamMaxLifetime(t *testing.T) {
	old := maxStreamLifetime
	maxStreamLifetime = 100 * time.Millisecond
	t.Cleanup(func() { maxStreamLifetime = old })

	srv := testServer(t, NewBroker(), 0)
	_, r := connect(t, srv, 1, "")
	r.Next(t, time.Second)
	if !r.Closed(time.Second) {
		t.Error("el stream debería cortarse al llegar a su vida máxima")
	}
}

// Un cliente que deja de leer sin cerrar la conexión (una pestaña colgada, una red que pierde
// paquetes sin avisar): la escritura se bloquea en cuanto se llena el buffer de TCP. Sin un plazo
// por escritura, el handler quedaba trabado ahí y no veía ni el logout ni el apagado.
func TestStuckClientIsReleased(t *testing.T) {
	old := streamWriteTimeout
	streamWriteTimeout = 200 * time.Millisecond
	t.Cleanup(func() { streamWriteTimeout = old })

	b := NewBroker()
	srv := testServer(t, b, 0)

	conn, err := net.Dial("tcp", srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	_ = conn.(*net.TCPConn).SetReadBuffer(4096)
	fmt.Fprint(conn, "GET /events HTTP/1.1\r\nHost: test\r\nX-Test-User: 1\r\nX-Test-Session: s1\r\n\r\n")
	// A partir de acá el cliente no lee nunca.

	deadline := time.Now().Add(2 * time.Second)
	for connections(b) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("la conexión no llegó a abrirse")
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Eventos grandes, de a uno, hasta que el handler deja de consumirlos: quedó trabado en Write
	// con los buffers de TCP llenos. (Sin pasar de la mitad del buffer del broker, para que no
	// la corte por cliente lento: ese no es el caso que se prueba.)
	sub := subscriptionOf(b, 1)
	big := strings.Repeat("x", 256<<10)
	deadline = time.Now().Add(10 * time.Second)
	for len(sub.events) < subscriptionBuffer/2 {
		if time.Now().After(deadline) {
			t.Fatal("el handler nunca se trabó: los buffers no se llenaron")
		}
		b.PublishToUser(t.Context(), 1, Event{Type: "big", Data: big})
		time.Sleep(5 * time.Millisecond)
	}

	// El logout tiene que liberar la conexión, aunque el cliente siga sin leer.
	b.SessionEnded(t.Context(), "s1")
	deadline = time.Now().Add(2 * time.Second)
	for connections(b) != 0 {
		if time.Now().After(deadline) {
			t.Fatal("la conexión trabada sigue ocupando su lugar después del logout")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func subscriptionOf(b *Broker, userID int64) *subscription {
	b.mu.Lock()
	defer b.mu.Unlock()
	for sub := range b.channels[UserChannel(userID)] {
		return sub
	}
	return nil
}

func connections(b *Broker) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.total
}
