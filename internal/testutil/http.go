package testutil

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/NoeOnDev/go-api-base/internal/server"
)

// APIError es el formato de error de la API, para leerlo en los tests.
type APIError struct {
	Error struct {
		Code    string            `json:"code"`
		Message string            `json:"message"`
		Fields  map[string]string `json:"fields"`
	} `json:"error"`
}

// NewAPI devuelve la API completa (rutas y middlewares) conectada a la base de pruebas.
func NewAPI(pool *pgxpool.Pool) http.Handler {
	return server.NewHandler(server.Deps{
		Logger: slog.New(slog.DiscardHandler),
		DB:     pool,
	})
}

// Do envía una petición a la API. cookie es la sesión del usuario, o nil para una petición sin sesión.
func Do(t *testing.T, api http.Handler, cookie *http.Cookie, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	if cookie != nil {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	api.ServeHTTP(w, r)
	return w
}

// Decode comprueba el status de la respuesta y decodifica su cuerpo JSON en T.
func Decode[T any](t *testing.T, w *httptest.ResponseRecorder, wantStatus int) T {
	t.Helper()
	if w.Code != wantStatus {
		t.Fatalf("status = %d, se esperaba %d; cuerpo: %s", w.Code, wantStatus, w.Body)
	}
	var v T
	if err := json.NewDecoder(w.Body).Decode(&v); err != nil {
		t.Fatalf("respuesta no es JSON válido: %v", err)
	}
	return v
}
