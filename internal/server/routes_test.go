package server_test

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/NoeOnDev/go-api-base/internal/config"
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

	var last *httptest.ResponseRecorder
	for range 6 {
		r := httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader(`{"email":"a@b.com","password":"x"}`))
		last = httptest.NewRecorder()
		api.ServeHTTP(last, r)
	}

	if last.Code != http.StatusTooManyRequests || errorCode(t, last) != "too_many_requests" {
		t.Errorf("sexto intento: status = %d, se esperaba 429", last.Code)
	}
	if last.Header().Get("Retry-After") == "" {
		t.Error("falta Retry-After")
	}
}
