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

const frontend = "https://app.ejemplo.com"

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
		t.Fatalf("respuesta no es JSON válido: %v", err)
	}
	return resp.Error.Code
}

func TestHealthz(t *testing.T) {
	api := newAPI(t)
	w := httptest.NewRecorder()
	api.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"status":"ok"`) {
		t.Errorf("status = %d, cuerpo = %s", w.Code, w.Body)
	}
	if w.Header().Get("X-Request-ID") == "" {
		t.Error("falta X-Request-ID en la respuesta")
	}
}

func TestUnknownRoute(t *testing.T) {
	api := newAPI(t)
	w := httptest.NewRecorder()
	api.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/no-existe", nil))

	if w.Code != http.StatusNotFound || errorCode(t, w) != "not_found" {
		t.Errorf("status = %d", w.Code)
	}
}

func TestCSRFProtection(t *testing.T) {
	api := newAPI(t)
	body := `{"email":"a@b.com","password":"x"}`

	t.Run("rechaza un POST de otro sitio", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader(body))
		r.Header.Set("Origin", "https://sitio-malicioso.com")
		r.Header.Set("Sec-Fetch-Site", "cross-site")
		w := httptest.NewRecorder()
		api.ServeHTTP(w, r)

		if w.Code != http.StatusForbidden || errorCode(t, w) != "forbidden" {
			t.Errorf("status = %d, se esperaba 403", w.Code)
		}
	})

	t.Run("acepta un POST del frontend configurado", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader(body))
		r.Header.Set("Origin", frontend)
		r.Header.Set("Sec-Fetch-Site", "cross-site")
		w := httptest.NewRecorder()
		api.ServeHTTP(w, r)

		// Llega al handler: credenciales incorrectas, no bloqueo por origen.
		if w.Code != http.StatusUnauthorized {
			t.Errorf("status = %d, se esperaba 401", w.Code)
		}
		if w.Header().Get("Access-Control-Allow-Origin") != frontend {
			t.Error("falta el encabezado CORS para el frontend")
		}
	})

	t.Run("acepta peticiones sin encabezados de navegador (curl, apps)", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader(body))
		w := httptest.NewRecorder()
		api.ServeHTTP(w, r)

		if w.Code != http.StatusUnauthorized {
			t.Errorf("status = %d, se esperaba 401", w.Code)
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
		t.Errorf("status = %d, encabezados = %v", w.Code, w.Header())
	}
}

func TestLoginRateLimit(t *testing.T) {
	api := newAPI(t)

	// 20 seguidos por IP: el número 21 se rechaza.
	var last *httptest.ResponseRecorder
	for range 21 {
		r := httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader(`{"email":"a@b.com","password":"x"}`))
		last = httptest.NewRecorder()
		api.ServeHTTP(last, r)
	}

	if last.Code != http.StatusTooManyRequests || errorCode(t, last) != "too_many_requests" {
		t.Errorf("intento 21: status = %d, se esperaba 429", last.Code)
	}
	if last.Header().Get("Retry-After") == "" {
		t.Error("falta Retry-After")
	}
	// El 429 del límite por IP también lleva los encabezados de /auth.
	if last.Header().Get("Cache-Control") != "no-store" || last.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("encabezados del 429: %v", last.Header())
	}
}

func TestSecurityHeaders(t *testing.T) {
	api := newAPI(t)

	for _, tt := range []struct {
		path    string
		noStore bool
	}{
		{"/auth/me", true}, // 401 sin sesión: igual no se guarda
		{"/products", false},
		{"/no-existe", false},
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

func TestEventsEndToEnd(t *testing.T) {
	pool := testutil.NewDB(t)
	broker := realtime.NewBroker()
	srv := testutil.NewServer(t, pool, testutil.WithBroker(broker))

	// Sin sesión no se puede escuchar.
	resp, err := srv.Client().Get(srv.URL + "/events")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("sin sesión: status = %d", resp.StatusCode)
	}

	// Con sesión, a través de todos los middlewares de la API, el evento llega.
	u, cookie := testutil.Login(t, pool, "ana@ejemplo.com", "user")
	events := testutil.OpenEvents(t, srv, cookie, "")
	broker.PublishToUser(t.Context(), u.ID, realtime.Event{Type: "notification", Data: "hola"})
	if ev := events.Next(t, 2*time.Second); ev.Type != "notification" || ev.Data != `"hola"` {
		t.Errorf("evento: %+v", ev)
	}
}
