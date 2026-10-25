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

// NewServer levanta la API en un servidor HTTP real, con los mismos timeouts que en producción.
// Hace falta para probar los streams SSE (GET /events): httptest.ResponseRecorder no permite
// leer la respuesta mientras se escribe. Acepta las mismas opciones que NewAPI.
func NewServer(t *testing.T, pool *pgxpool.Pool, opts ...Option) *httptest.Server {
	t.Helper()
	srv := httptest.NewUnstartedServer(NewAPI(pool, opts...))
	srv.Config = server.New("", srv.Config.Handler, slog.New(slog.DiscardHandler))
	srv.Start()
	t.Cleanup(srv.Close)
	return srv
}

// OpenEvents abre GET /events con la sesión del usuario y devuelve un lector de sus eventos.
// topics es la lista para ?topics= (vacía para escuchar solo el canal del usuario). El primer
// evento ("ready") ya se leyó.
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
		t.Fatalf("se esperaba el evento ready: %+v", ev)
	}
	return r
}
