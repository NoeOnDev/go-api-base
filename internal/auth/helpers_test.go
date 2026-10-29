package auth_test

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/NoeOnDev/go-api-base/internal/auth"
	"github.com/NoeOnDev/go-api-base/internal/config"
	"github.com/NoeOnDev/go-api-base/internal/secretbox"
	"github.com/NoeOnDev/go-api-base/internal/server"
	"github.com/NoeOnDev/go-api-base/internal/testutil"
)

// Ayudas que comparten los tests de varias funciones de auth. Están acá, y no en el test de una
// función, para que quitar una función con sus tests (ver "Quitar funciones" en docs/auth.md) no
// rompa los tests de las demás.

type apiError struct {
	Error struct {
		Code   string            `json:"code"`
		Fields map[string]string `json:"fields"`
	} `json:"error"`
}

func newAPI(t *testing.T) (http.Handler, *pgxpool.Pool) {
	t.Helper()
	pool := testutil.NewDB(t)
	box, err := secretbox.New(testutil.EncryptionKey)
	if err != nil {
		t.Fatal(err)
	}
	api := server.NewHandler(server.Deps{
		Logger: slog.New(slog.DiscardHandler),
		DB:     pool,
		Config: config.Config{SessionTTL: time.Hour},
		Box:    box,
	})
	return api, pool
}

func do(t *testing.T, api http.Handler, method, path, body string, cookie *http.Cookie) *httptest.ResponseRecorder {
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

func decode[T any](t *testing.T, w *httptest.ResponseRecorder, wantStatus int) T {
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

func sessionCookie(t *testing.T, w *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	for _, c := range w.Result().Cookies() {
		if c.Name == auth.CookieName {
			return c
		}
	}
	t.Fatal("la respuesta no tiene cookie de sesión")
	return nil
}

// newSession inicia otra sesión para el mismo usuario, como si entrara desde otro dispositivo.
func newSession(t *testing.T, pool *pgxpool.Pool, userID int64) *http.Cookie {
	t.Helper()
	w := httptest.NewRecorder()
	if err := auth.NewService(pool, auth.Options{}).StartSession(w, httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/auth/login", nil), userID); err != nil {
		t.Fatalf("iniciar sesión: %v", err)
	}
	return sessionCookie(t, w)
}

// countRows cuenta las filas del usuario en la tabla (en users, la del propio usuario).
func countRows(t *testing.T, pool *pgxpool.Pool, table string, userID int64) int {
	t.Helper()
	column := "user_id"
	if table == "users" {
		column = "id"
	}
	var n int
	if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM "+table+" WHERE "+column+" = $1", userID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

const newPassword = "a-new-password"

func resetBody(token, password string) string {
	return fmt.Sprintf(`{"token":%q,"password":%q}`, token, password)
}

func requireVerification(c *config.Config) { c.RequireEmailVerification = true }

const loginBody = `{"email":"alice@example.com","password":"test-password"}`

// ips entrega una IP nueva en cada llamada.
type ips struct{ n int }

func (p *ips) next() string {
	p.n++
	return fmt.Sprintf("198.51.100.%d", p.n)
}

// failSessionDeletes hace que la base rechace borrar sesiones, para simular una falla a mitad
// de una operación. Se deshace al terminar el test.
func failSessionDeletes(t *testing.T, exec func(sql string) error) {
	t.Helper()
	t.Cleanup(func() {
		_ = exec("DROP TRIGGER IF EXISTS test_fail_session_delete ON sessions")
		_ = exec("DROP FUNCTION IF EXISTS test_fail_session_delete()")
	})
	for _, sql := range []string{
		`CREATE FUNCTION test_fail_session_delete() RETURNS trigger LANGUAGE plpgsql
		 AS $$ BEGIN RAISE EXCEPTION 'falla de prueba'; END $$`,
		`CREATE TRIGGER test_fail_session_delete BEFORE DELETE ON sessions
		 FOR EACH ROW EXECUTE FUNCTION test_fail_session_delete()`,
	} {
		if err := exec(sql); err != nil {
			t.Fatal(err)
		}
	}
}
