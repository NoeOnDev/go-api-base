package auth_test

import (
	"crypto/sha256"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/NoeOnDev/go-api-base/internal/auth"
	"github.com/NoeOnDev/go-api-base/internal/config"
	"github.com/NoeOnDev/go-api-base/internal/server"
	"github.com/NoeOnDev/go-api-base/internal/store"
	"github.com/NoeOnDev/go-api-base/internal/testutil"
)

type apiError struct {
	Error struct {
		Code   string            `json:"code"`
		Fields map[string]string `json:"fields"`
	} `json:"error"`
}

func newAPI(t *testing.T) (http.Handler, *pgxpool.Pool) {
	t.Helper()
	pool := testutil.NewDB(t)
	api := server.NewHandler(server.Deps{
		Logger: slog.New(slog.DiscardHandler),
		DB:     pool,
		Config: config.Config{SessionTTL: time.Hour, EncryptionKey: testutil.EncryptionKey},
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

const registerBody = `{"name":"Ana","email":"Ana@Ejemplo.com","password":"una-clave-larga"}`

func TestRegisterLoginLogout(t *testing.T) {
	api, _ := newAPI(t)

	// Registro: crea el usuario con rol user, email en minúsculas, e inicia la sesión.
	w := do(t, api, http.MethodPost, "/auth/register", registerBody, nil)
	u := decode[auth.User](t, w, http.StatusCreated)
	if u.Email != "ana@ejemplo.com" || u.Role != auth.RoleUser || u.ID == 0 {
		t.Fatalf("usuario inesperado: %+v", u)
	}
	if strings.Contains(w.Body.String(), "password") {
		t.Error("la respuesta expone datos de la contraseña")
	}

	c := sessionCookie(t, w)
	if !c.HttpOnly || c.SameSite != http.SameSiteLaxMode || c.Path != "/" {
		t.Errorf("cookie con atributos inseguros: %+v", c)
	}

	// La sesión identifica al usuario.
	me := decode[auth.User](t, do(t, api, http.MethodGet, "/auth/me", "", c), http.StatusOK)
	if me.ID != u.ID {
		t.Errorf("me = %+v, se esperaba el usuario %d", me, u.ID)
	}

	// Logout: borra la cookie en el navegador y la sesión en la base.
	w = do(t, api, http.MethodPost, "/auth/logout", "", c)
	if w.Code != http.StatusNoContent {
		t.Fatalf("logout status = %d", w.Code)
	}
	if cleared := sessionCookie(t, w); cleared.MaxAge >= 0 {
		t.Errorf("la cookie no se borró: %+v", cleared)
	}
	if w := do(t, api, http.MethodGet, "/auth/me", "", c); w.Code != http.StatusUnauthorized {
		t.Errorf("la sesión sigue activa después del logout: status = %d", w.Code)
	}

	// Login con el email en otra combinación de mayúsculas.
	w = do(t, api, http.MethodPost, "/auth/login", `{"email":"ANA@ejemplo.com","password":"una-clave-larga"}`, nil)
	decode[auth.User](t, w, http.StatusOK)
	if w := do(t, api, http.MethodGet, "/auth/me", "", sessionCookie(t, w)); w.Code != http.StatusOK {
		t.Errorf("me después del login: status = %d", w.Code)
	}
}

func TestRegisterValidation(t *testing.T) {
	api, _ := newAPI(t)

	tests := []struct {
		name, body, field string
	}{
		{"sin nombre", `{"name":" ","email":"a@b.com","password":"12345678"}`, "name"},
		{"email inválido", `{"name":"Ana","email":"no-es-email","password":"12345678"}`, "email"},
		{"email con nombre", `{"name":"Ana","email":"Ana <a@b.com>","password":"12345678"}`, "email"},
		{"email sin dominio", `{"name":"Ana","email":"a@localhost","password":"12345678"}`, "email"},
		{"contraseña corta", `{"name":"Ana","email":"a@b.com","password":"1234567"}`, "password"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := decode[apiError](t, do(t, api, http.MethodPost, "/auth/register", tt.body, nil), http.StatusUnprocessableEntity)
			if resp.Error.Fields[tt.field] == "" {
				t.Errorf("se esperaba error en %q: %+v", tt.field, resp.Error)
			}
		})
	}
}

func TestRegisterDuplicateEmail(t *testing.T) {
	api, _ := newAPI(t)

	decode[auth.User](t, do(t, api, http.MethodPost, "/auth/register", registerBody, nil), http.StatusCreated)

	dup := `{"name":"Otra Ana","email":"ana@EJEMPLO.com","password":"otra-clave-larga"}`
	resp := decode[apiError](t, do(t, api, http.MethodPost, "/auth/register", dup, nil), http.StatusConflict)
	if resp.Error.Code != "conflict" || resp.Error.Fields["email"] == "" {
		t.Errorf("error inesperado: %+v", resp.Error)
	}
}

func TestLoginFailures(t *testing.T) {
	api, _ := newAPI(t)
	decode[auth.User](t, do(t, api, http.MethodPost, "/auth/register", registerBody, nil), http.StatusCreated)

	// Contraseña incorrecta y email inexistente responden exactamente igual.
	wrongPass := do(t, api, http.MethodPost, "/auth/login", `{"email":"ana@ejemplo.com","password":"incorrecta"}`, nil)
	unknown := do(t, api, http.MethodPost, "/auth/login", `{"email":"nadie@ejemplo.com","password":"incorrecta"}`, nil)
	if wrongPass.Code != http.StatusUnauthorized || unknown.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d y %d, se esperaba 401", wrongPass.Code, unknown.Code)
	}
	if wrongPass.Body.String() != unknown.Body.String() {
		t.Errorf("las respuestas difieren y revelan si el email existe:\n%s\n%s", wrongPass.Body, unknown.Body)
	}

	resp := decode[apiError](t, do(t, api, http.MethodPost, "/auth/login", `{}`, nil), http.StatusUnprocessableEntity)
	if resp.Error.Fields["email"] == "" || resp.Error.Fields["password"] == "" {
		t.Errorf("error inesperado: %+v", resp.Error)
	}
}

func TestRequireUserRejectsInvalidSessions(t *testing.T) {
	api, _ := newAPI(t)

	for name, c := range map[string]*http.Cookie{
		"sin cookie":      nil,
		"token inventado": {Name: auth.CookieName, Value: "token-inventado"},
	} {
		t.Run(name, func(t *testing.T) {
			resp := decode[apiError](t, do(t, api, http.MethodGet, "/auth/me", "", c), http.StatusUnauthorized)
			if resp.Error.Code != "unauthorized" {
				t.Errorf("code = %q", resp.Error.Code)
			}
		})
	}
}

func TestSessionTokenIsStoredHashed(t *testing.T) {
	api, pool := newAPI(t)
	c := sessionCookie(t, do(t, api, http.MethodPost, "/auth/register", registerBody, nil))

	sum := sha256.Sum256([]byte(c.Value))
	var plain, hashed int
	if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM sessions WHERE token_hash = $1", []byte(c.Value)).Scan(&plain); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM sessions WHERE token_hash = $1", sum[:]).Scan(&hashed); err != nil {
		t.Fatal(err)
	}
	if plain != 0 || hashed != 1 {
		t.Errorf("token en texto plano: %d filas, hasheado: %d filas", plain, hashed)
	}
}

func TestRequireAdmin(t *testing.T) {
	pool := testutil.NewDB(t)
	service := auth.NewService(store.New(pool), auth.Options{})

	mux := http.NewServeMux()
	mux.Handle("GET /admin", service.RequireAdmin(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})))

	loginAs := func(email, role string) *http.Cookie {
		u, err := service.CreateUser(t.Context(), auth.RegisterInput{Name: "x", Email: email, Password: "una-clave-larga"}, role)
		if err != nil {
			t.Fatalf("crear usuario: %v", err)
		}
		w := httptest.NewRecorder()
		if err := service.StartSession(t.Context(), w, u.ID); err != nil {
			t.Fatalf("iniciar sesión: %v", err)
		}
		return sessionCookie(t, w)
	}

	tests := []struct {
		name   string
		cookie *http.Cookie
		want   int
	}{
		{"sin sesión", nil, http.StatusUnauthorized},
		{"usuario común", loginAs("user@ejemplo.com", auth.RoleUser), http.StatusForbidden},
		{"administrador", loginAs("admin@ejemplo.com", auth.RoleAdmin), http.StatusOK},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if w := do(t, mux, http.MethodGet, "/admin", "", tt.cookie); w.Code != tt.want {
				t.Errorf("status = %d, se esperaba %d", w.Code, tt.want)
			}
		})
	}
}

func TestLogoutAlwaysClearsCookie(t *testing.T) {
	api, _ := newAPI(t)

	for name, c := range map[string]*http.Cookie{
		"sin cookie":     nil,
		"sesión vencida": {Name: auth.CookieName, Value: "token-que-ya-no-existe"},
	} {
		t.Run(name, func(t *testing.T) {
			w := do(t, api, http.MethodPost, "/auth/logout", "", c)
			if w.Code != http.StatusNoContent {
				t.Fatalf("status = %d, se esperaba 204", w.Code)
			}
			if cleared := sessionCookie(t, w); cleared.MaxAge >= 0 {
				t.Errorf("la cookie no se borró: %+v", cleared)
			}
		})
	}
}
