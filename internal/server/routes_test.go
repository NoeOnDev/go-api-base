package server_test

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/NoeOnDev/go-api-base/internal/config"
	"github.com/NoeOnDev/go-api-base/internal/realtime"
	"github.com/NoeOnDev/go-api-base/internal/server"
	"github.com/NoeOnDev/go-api-base/internal/testutil"
)

const frontend = "https://app.example.com"

func newAPI(t *testing.T) http.Handler {
	t.Helper()
	pool := testutil.NewDB(t)
	return server.NewHandler(server.Deps{
		Logger: slog.New(slog.DiscardHandler),
		DB:     pool,
		Config: config.Config{CORSAllowedOrigins: []string{frontend}},
	})
}

func errorCode(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var resp struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("the response is not valid JSON: %v", err)
	}
	return resp.Error.Code
}

func TestHealthz(t *testing.T) {
	api := newAPI(t)
	w := httptest.NewRecorder()
	api.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"status":"ok"`) {
		t.Errorf("status = %d, body = %s", w.Code, w.Body)
	}
	if w.Header().Get("X-Request-ID") == "" {
		t.Error("missing X-Request-ID in the response")
	}
}

func TestUnknownRoute(t *testing.T) {
	api := newAPI(t)
	w := httptest.NewRecorder()
	api.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/missing", nil))

	if w.Code != http.StatusNotFound || errorCode(t, w) != "route_not_found" {
		t.Errorf("status = %d", w.Code)
	}
}

func TestCSRFProtection(t *testing.T) {
	api := newAPI(t)
	body := `{"email":"a@b.com","password":"x"}`

	t.Run("rejects a POST from another site", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader(body))
		r.Header.Set("Origin", "https://sitio-malicioso.com")
		r.Header.Set("Sec-Fetch-Site", "cross-site")
		w := httptest.NewRecorder()
		api.ServeHTTP(w, r)

		if w.Code != http.StatusForbidden || errorCode(t, w) != "cross_origin_rejected" {
			t.Errorf("status = %d, want 403", w.Code)
		}
	})

	t.Run("accepts a POST from the configured frontend", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader(body))
		r.Header.Set("Origin", frontend)
		r.Header.Set("Sec-Fetch-Site", "cross-site")
		w := httptest.NewRecorder()
		api.ServeHTTP(w, r)

		// It reaches the handler: wrong credentials, not an origin block.
		if w.Code != http.StatusUnauthorized {
			t.Errorf("status = %d, want 401", w.Code)
		}
		if w.Header().Get("Access-Control-Allow-Origin") != frontend {
			t.Error("missing the CORS header for the frontend")
		}
	})

	t.Run("accepts requests without browser headers (curl, apps)", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader(body))
		w := httptest.NewRecorder()
		api.ServeHTTP(w, r)

		if w.Code != http.StatusUnauthorized {
			t.Errorf("status = %d, want 401", w.Code)
		}
	})
}

func TestCORSPreflight(t *testing.T) {
	api := newAPI(t)
	r := httptest.NewRequest(http.MethodOptions, "/notes", nil)
	r.Header.Set("Origin", frontend)
	r.Header.Set("Access-Control-Request-Method", "POST")
	w := httptest.NewRecorder()
	api.ServeHTTP(w, r)

	if w.Code != http.StatusNoContent || w.Header().Get("Access-Control-Allow-Origin") != frontend {
		t.Errorf("status = %d, headers = %v", w.Code, w.Header())
	}
}

func TestLoginRateLimit(t *testing.T) {
	api := newAPI(t)

	// 20 in a row per IP: number 21 is rejected.
	var last *httptest.ResponseRecorder
	for range 21 {
		r := httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader(`{"email":"a@b.com","password":"x"}`))
		last = httptest.NewRecorder()
		api.ServeHTTP(last, r)
	}

	if last.Code != http.StatusTooManyRequests || errorCode(t, last) != "too_many_requests" {
		t.Errorf("attempt 21: status = %d, want 429", last.Code)
	}
	if last.Header().Get("Retry-After") == "" {
		t.Error("missing Retry-After")
	}
	// The 429 of the per-IP limit carries the /auth headers too.
	if last.Header().Get("Cache-Control") != "no-store" || last.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("headers of the 429: %v", last.Header())
	}
}

func TestGeneralRateLimit(t *testing.T) {
	pool := testutil.NewDB(t)
	api := testutil.NewAPI(pool)

	// 200 in a row per IP, on any route: number 201 is rejected.
	for i := range 200 {
		if w := testutil.Do(t, api, nil, http.MethodGet, "/categories", ""); w.Code != http.StatusOK {
			t.Fatalf("request %d: status = %d", i+1, w.Code)
		}
	}
	w := testutil.Do(t, api, nil, http.MethodGet, "/categories", "")
	if w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") == "" {
		t.Errorf("request 201: status = %d, Retry-After = %q; want 429 with Retry-After", w.Code, w.Header().Get("Retry-After"))
	}

	// Another IP has its own attempts, and /healthz is not limited.
	if w := testutil.DoFromIP(t, api, "198.51.100.7", nil, http.MethodGet, "/categories", ""); w.Code != http.StatusOK {
		t.Errorf("another IP: status = %d", w.Code)
	}
	if w := testutil.Do(t, api, nil, http.MethodGet, "/healthz", ""); w.Code != http.StatusOK {
		t.Errorf("/healthz: status = %d", w.Code)
	}
}

func TestSecurityHeaders(t *testing.T) {
	api := newAPI(t)

	for _, tt := range []struct {
		path    string
		noStore bool
	}{
		{"/auth/me", true}, // 401 without a session: still not cached
		{"/products", false},
		{"/missing", false},
	} {
		w := httptest.NewRecorder()
		api.ServeHTTP(w, httptest.NewRequest(http.MethodGet, tt.path, nil))
		if got := w.Header().Get("X-Content-Type-Options"); got != "nosniff" {
			t.Errorf("%s: X-Content-Type-Options = %q", tt.path, got)
		}
		if got := w.Header().Get("Cache-Control") == "no-store"; got != tt.noStore {
			t.Errorf("%s: Cache-Control = %q", tt.path, w.Header().Get("Cache-Control"))
		}
	}
}

func TestHSTSOnlyInProduction(t *testing.T) {
	pool := testutil.NewDB(t)
	production := func(secure bool) testutil.Option {
		return testutil.WithConfig(func(c *config.Config) {
			c.Env = config.EnvProduction
			c.CookieSecure = secure
		})
	}

	for name, tt := range map[string]struct {
		api  http.Handler
		want string
	}{
		"production with HTTPS":    {testutil.NewAPI(pool, production(true)), "max-age=31536000"},
		"production without HTTPS": {testutil.NewAPI(pool, production(false)), ""},
		"development":              {testutil.NewAPI(pool), ""},
	} {
		w := testutil.Do(t, tt.api, nil, http.MethodGet, "/missing", "")
		if got := w.Header().Get("Strict-Transport-Security"); got != tt.want {
			t.Errorf("%s: Strict-Transport-Security = %q, want %q", name, got, tt.want)
		}
	}
}

func TestEventsEndToEnd(t *testing.T) {
	pool := testutil.NewDB(t)
	broker := realtime.NewBroker()
	srv := testutil.NewServer(t, pool, testutil.WithBroker(broker))

	// Without a session there is no listening.
	resp, err := srv.Client().Get(srv.URL + "/events")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("without a session: status = %d", resp.StatusCode)
	}

	// With a session, through every middleware of the API, the event arrives.
	u, cookie := testutil.Login(t, pool, "alice@example.com", "user")
	events := testutil.OpenEvents(t, srv, cookie, "")
	broker.PublishToUser(t.Context(), u.ID, realtime.Event{Type: "notification", Data: "hello"})
	if ev := events.Next(t, 2*time.Second); ev.Type != "notification" || ev.Data != `"hello"` {
		t.Errorf("event: %+v", ev)
	}
}
