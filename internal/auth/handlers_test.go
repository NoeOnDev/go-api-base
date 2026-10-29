package auth_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"golang.org/x/crypto/argon2"

	"github.com/NoeOnDev/go-api-base/internal/auth"
	"github.com/NoeOnDev/go-api-base/internal/mail"
	"github.com/NoeOnDev/go-api-base/internal/testutil"
)

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
	if resp.Error.Code != "email_taken" || resp.Error.Fields["email"] == "" {
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
	service := auth.NewService(pool, auth.Options{})

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
		if err := service.StartSession(w, httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/auth/login", nil), u.ID); err != nil {
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

// Una contraseña guardada con parámetros viejos de argon2 (por ejemplo, antes de subirlos) se
// vuelve a hashear con los actuales al entrar, y sigue sirviendo.
func TestLoginRehashesOldPassword(t *testing.T) {
	api, pool := newAPI(t)
	u, _ := testutil.Login(t, pool, "ana@ejemplo.com", auth.RoleUser)

	salt := []byte("sal-de-16-bytes!")
	key := argon2.IDKey([]byte(testutil.Password), salt, 1, 8*1024, 1, 32)
	old := fmt.Sprintf("$argon2id$v=%d$m=8192,t=1,p=1$%s$%s", argon2.Version,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key))
	if _, err := pool.Exec(t.Context(), "UPDATE users SET password_hash = $1 WHERE id = $2", old, u.ID); err != nil {
		t.Fatal(err)
	}

	if w := testutil.Do(t, api, nil, http.MethodPost, "/auth/login", loginBody); w.Code != http.StatusOK {
		t.Fatalf("login con el hash viejo: status = %d; cuerpo: %s", w.Code, w.Body)
	}
	var stored string
	if err := pool.QueryRow(t.Context(), "SELECT password_hash FROM users WHERE id = $1", u.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stored, "$m=19456,t=2,p=1$") {
		t.Errorf("el hash no se actualizó a los parámetros actuales: %s", stored)
	}
	if w := testutil.Do(t, api, nil, http.MethodPost, "/auth/login", loginBody); w.Code != http.StatusOK {
		t.Errorf("login con el hash nuevo: status = %d", w.Code)
	}
}

// La base exige el email en minúsculas: UNIQUE solo evita duplicados si nadie guarda
// "Ana@Ejemplo.com" junto a "ana@ejemplo.com", por ejemplo con un INSERT a mano.
func TestEmailIsLowercaseInDatabase(t *testing.T) {
	_, pool := newAPI(t)
	_, err := pool.Exec(t.Context(), "INSERT INTO users (name, email, password_hash) VALUES ('Ana', 'Ana@Ejemplo.com', 'x')")
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23514" || pgErr.ConstraintName != "users_email_lowercase" {
		t.Fatalf("se esperaba la violación de users_email_lowercase, llegó %v", err)
	}
}

// Si el correo de verificación no se puede encolar, la cuenta igual quedó creada: se responde
// 201, y el usuario puede pedir otro enlace.
func TestRegisterSucceedsWhenVerificationEmailFails(t *testing.T) {
	pool := testutil.NewDB(t)
	api := testutil.NewAPI(pool, testutil.WithMailer(failingMailer{}), testutil.WithConfig(requireVerification))

	w := testutil.Do(t, api, nil, http.MethodPost, "/auth/register", registerBody)
	u := testutil.Decode[auth.User](t, w, http.StatusCreated)
	if u.EmailVerified() {
		t.Error("el email no debería estar verificado")
	}
	sessionCookie(t, w) // y quedó la sesión
}

type failingMailer struct{}

func (failingMailer) Send(context.Context, mail.Message) error { return errors.New("cola caída") }
