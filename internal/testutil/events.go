package testutil

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/NoeOnDev/go-api-base/internal/realtime/ssetest"
	"github.com/NoeOnDev/go-api-base/internal/server"
)

// NewServer runs the API on a real HTTP server, with the same timeouts as in production.
// It is needed to test the SSE streams (GET /events): httptest.ResponseRecorder does not let you
// read the response while it is written. It takes the same options as NewAPI.
func NewServer(t *testing.T, pool *pgxpool.Pool, opts ...Option) *httptest.Server {
	t.Helper()
	srv := httptest.NewUnstartedServer(NewAPI(pool, opts...))
	srv.Config = server.New("", srv.Config.Handler, slog.New(slog.DiscardHandler))
	srv.Start()
	t.Cleanup(srv.Close)
	return srv
}

// OpenEvents opens GET /events with the user's session and returns a reader of its events.
// topics is the list for ?topics= (empty to listen only to the user's channel). The first event
// ("ready") has already been read.
func OpenEvents(t *testing.T, srv *httptest.Server, cookie *http.Cookie, topics string) *ssetest.Reader {
	t.Helper()
	url := srv.URL + "/events"
	if topics != "" {
		url += "?topics=" + topics
	}
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	req.AddCookie(cookie)
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /events: status = %d", resp.StatusCode)
	}

	r := ssetest.NewReader(resp.Body)
	if ev := r.Next(t, 2*time.Second); ev.Type != "ready" {
		t.Fatalf("want the ready event: %+v", ev)
	}
	return r
}
