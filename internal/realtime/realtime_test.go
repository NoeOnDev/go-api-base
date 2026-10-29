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
	if err := s.Send(Event{ID: "7", Type: "note.updated", Data: map[string]any{"id": 7, "title": "line 1\nline 2"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.Comment("ping"); err != nil {
		t.Fatal(err)
	}

	// The line break of the data travels escaped inside the JSON: it does not cut the event.
	want := "id: 7\nevent: note.updated\ndata: {\"id\":7,\"title\":\"line 1\\nline 2\"}\n\n: ping\n\n"
	if got := w.Body.String(); got != want {
		t.Errorf("SSE format:\n%q\nwant:\n%q", got, want)
	}
	for k, v := range map[string]string{"Content-Type": "text/event-stream", "Cache-Control": "no-store", "X-Accel-Buffering": "no"} {
		if got := w.Header().Get(k); got != v {
			t.Errorf("%s = %q", k, got)
		}
	}

	// A line break in the type would inject another field: it is rejected.
	if err := s.Send(Event{Type: "x\ndata: inyectado"}); err == nil {
		t.Error("want an error for the line break in the type")
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
		t.Errorf("third connection of the user: %v", err)
	}
	if _, err := b.subscribe(2, "s2", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := b.subscribe(3, "s3", nil); !errors.Is(err, ErrTooManyConnections) {
		t.Errorf("total exceeded: %v", err)
	}

	// Closing a connection frees its slot.
	b.unsubscribe(a1)
	if _, err := b.subscribe(3, "s3", nil); err != nil {
		t.Errorf("after freeing a slot: %v", err)
	}
}

func TestBrokerClosesSlowSubscriber(t *testing.T) {
	b := NewBroker()
	sub, _ := b.subscribe(1, "s1", nil)

	// Nobody reads: when the buffer fills up, the connection is closed instead of holding back the others.
	for range subscriptionBuffer + 1 {
		b.PublishToUser(t.Context(), 1, Event{Type: "x"})
	}
	select {
	case <-sub.closed:
	default:
		t.Error("the slow connection should have been closed")
	}
}

func TestBrokerClose(t *testing.T) {
	b := NewBroker()
	sub, _ := b.subscribe(1, "s1", nil)
	b.Close()
	b.Close() // closing twice does not fail

	select {
	case <-sub.closed:
	default:
		t.Error("Close should close the open connections")
	}
	if _, err := b.subscribe(1, "s1", nil); !errors.Is(err, ErrClosed) {
		t.Errorf("after Close: %v", err)
	}
}

// fakeSessions replaces auth: the session is the X-Test-Session header, and those in ended are
// considered closed.
type fakeSessions struct {
	mu    sync.Mutex
	ended map[string]bool
}

func (f *fakeSessions) SessionKey(r *http.Request) string { return r.Header.Get("X-Test-Session") }

func (f *fakeSessions) CheckSession(r *http.Request) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ended[f.SessionKey(r)] {
		return errors.New("session closed")
	}
	return nil
}

func (f *fakeSessions) end(key string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ended[key] = true
}

// testServer runs the handler on a real HTTP server, with the given WriteTimeout.
// The user is chosen with the X-Test-User header (in the app RequireUser sets it), and the
// session with X-Test-Session (by default, "s<user>").
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
		return userID == 1 && id == "42", nil // only user 1 is part of project 42
	})
	// A short WriteTimeout: without removing it, the server would cut the stream right away.
	srv := testServer(t, b, 200*time.Millisecond)

	resp, alice := connect(t, srv, 1, "?topics=project:42")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if ev := alice.Next(t, time.Second); ev.Type != "ready" || ev.Data != `{"topics":["project:42"],"rejected":[]}` {
		t.Fatalf("first event: %+v", ev)
	}
	_, bob := connect(t, srv, 2, "")
	if ev := bob.Next(t, time.Second); ev.Data != `{"topics":[],"rejected":[]}` {
		t.Errorf("ready without topics: %+v", ev)
	}

	// Past the WriteTimeout, the stream is still alive, with heartbeats.
	time.Sleep(300 * time.Millisecond)
	if c := alice.NextComment(t, time.Second); c != "ping" {
		t.Errorf("latido = %q", c)
	}

	// Each one gets what belongs to its channel and topics, and nothing else.
	b.PublishToUser(t.Context(), 1, Event{Type: "notification", Data: "hola Alice"})
	b.Publish(t.Context(), "project:42", Event{Type: "task.moved", Data: 5})
	if ev := alice.Next(t, time.Second); ev.Type != "notification" || ev.Data != `"hola Alice"` {
		t.Errorf("event of the user: %+v", ev)
	}
	if ev := alice.Next(t, time.Second); ev.Type != "task.moved" || ev.Data != "5" {
		t.Errorf("event of the topic: %+v", ev)
	}
	bob.Nothing(t, 100*time.Millisecond)
}

func TestEventsHandlerRejectedTopics(t *testing.T) {
	b := NewBroker()
	b.Authorize("project", func(_ context.Context, _ int64, id string) (bool, error) { return id == "1", nil })
	srv := testServer(t, b, 0)

	// A topic without permission does not cut the connection: it is reported in ready, and the rest
	// keeps working.
	resp, r := connect(t, srv, 1, "?topics=project:1,project:42,invoice:1,user:2,project")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	want := `{"topics":["project:1"],"rejected":["project:42","invoice:1","user:2","project"]}`
	if ev := r.Next(t, time.Second); ev.Type != "ready" || ev.Data != want {
		t.Fatalf("ready = %+v, want %s", ev, want)
	}

	// It gets its channel and the allowed topic; nothing from the rejected ones.
	b.Publish(t.Context(), "project:42", Event{Type: "ajeno"})
	b.Publish(t.Context(), "user:2", Event{Type: "ajeno"})
	b.PublishToUser(t.Context(), 1, Event{Type: "notification"})
	b.Publish(t.Context(), "project:1", Event{Type: "task.moved"})
	if ev := r.Next(t, time.Second); ev.Type != "notification" {
		t.Errorf("event of the user: %+v", ev)
	}
	if ev := r.Next(t, time.Second); ev.Type != "task.moved" {
		t.Errorf("event of the allowed topic: %+v", ev)
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
		t.Errorf("second connection with limit 1: status = %d", resp.StatusCode)
	}
}

func TestStreamLifetimeVaries(t *testing.T) {
	seen := map[time.Duration]bool{}
	for range 100 {
		d := streamLifetime()
		if d < 13*time.Minute || d > 17*time.Minute {
			t.Fatalf("stream life outside 13 to 17 minutes: %v", d)
		}
		seen[d] = true
	}
	if len(seen) < 50 {
		t.Errorf("the stream life barely varies: %d different values in 100", len(seen))
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
	// Without RegisterOnShutdown(b.Close), Shutdown would wait the whole timeout.
	if d := time.Since(start); d > time.Second {
		t.Errorf("the shutdown took %v", d)
	}
	if !r.Closed(time.Second) {
		t.Error("the stream should have closed on shutdown")
	}
}

func TestStreamsEndWithTheirSession(t *testing.T) {
	b := NewBroker()
	srv := testServer(t, b, 0)

	// Alice has two sessions (two devices), and Bob one.
	_, phone := connect(t, srv, 1, "", "telefono")
	_, laptop := connect(t, srv, 1, "", "laptop")
	_, bob := connect(t, srv, 2, "")
	for _, r := range []*ssetest.Reader{phone, laptop, bob} {
		r.Next(t, time.Second) // ready
	}

	// Logout on the phone: only that stream is cut.
	b.SessionEnded(t.Context(), "telefono")
	if !phone.Closed(time.Second) {
		t.Error("the phone stream should be cut by the logout")
	}
	if laptop.Closed(100 * time.Millisecond) {
		t.Error("the laptop stream should not be cut")
	}

	// Alice changes the password from the laptop: the other sessions are cut, not hers.
	_, other := connect(t, srv, 1, "", "sesion-robada")
	other.Next(t, time.Second)
	b.UserSessionsEnded(t.Context(), 1, "laptop")
	if !other.Closed(time.Second) {
		t.Error("the stream of the other session should be cut")
	}
	if laptop.Closed(100 * time.Millisecond) {
		t.Error("the stream of the remaining session should not be cut")
	}

	// A password reset closes all of them.
	b.UserSessionsEnded(t.Context(), 1, "")
	if !laptop.Closed(time.Second) {
		t.Error("a password reset should cut every stream of the user")
	}
	if bob.Closed(100 * time.Millisecond) {
		t.Error("the streams of another user are left alone")
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

	// The session was closed without telling the broker (for example, it expired or was deleted
	// with SQL): the next heartbeat notices and cuts the stream.
	sessions.end("s1")
	if !r.Closed(time.Second) {
		t.Error("the stream should be cut on the next heartbeat")
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
		t.Error("the stream should be cut when it reaches its maximum life")
	}
}

// A client that stops reading without closing the connection (a hung tab, a network that drops
// packets silently): the write blocks as soon as the TCP buffer fills up. Without a deadline per
// write, the handler got stuck there and saw neither the logout nor the shutdown.
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
	// From here on the client never reads.

	deadline := time.Now().Add(2 * time.Second)
	for connections(b) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the connection never opened")
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Large events, one at a time, until the handler stops consuming them: it got stuck in Write
	// with the TCP buffers full. (Without going past half of the broker's buffer, so it does not cut
	// it as a slow client: that is not the case being tested.)
	sub := subscriptionOf(b, 1)
	big := strings.Repeat("x", 256<<10)
	deadline = time.Now().Add(10 * time.Second)
	for len(sub.events) < subscriptionBuffer/2 {
		if time.Now().After(deadline) {
			t.Fatal("the handler never got stuck: the buffers did not fill up")
		}
		b.PublishToUser(t.Context(), 1, Event{Type: "big", Data: big})
		time.Sleep(5 * time.Millisecond)
	}

	// The logout has to free the connection, even if the client still does not read.
	b.SessionEnded(t.Context(), "s1")
	deadline = time.Now().Add(2 * time.Second)
	for connections(b) != 0 {
		if time.Now().After(deadline) {
			t.Fatal("the stuck connection still takes its slot after the logout")
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
