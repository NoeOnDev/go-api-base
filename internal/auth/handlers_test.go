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

const registerBody = `{"name":"Alice","email":"Alice@Example.com","password":"a-long-password"}`

func TestRegisterLoginLogout(t *testing.T) {
	api, _ := newAPI(t)

	// Registration: it creates the user with role user and a lowercase email, and starts the session.
	w := do(t, api, http.MethodPost, "/auth/register", registerBody, nil)
	u := decode[auth.User](t, w, http.StatusCreated)
	if u.Email != "alice@example.com" || u.Role != auth.RoleUser || u.ID == 0 {
		t.Fatalf("unexpected user: %+v", u)
	}
	if strings.Contains(w.Body.String(), "password") {
		t.Error("the response exposes password data")
	}

	c := sessionCookie(t, w)
	if !c.HttpOnly || c.SameSite != http.SameSiteLaxMode || c.Path != "/" {
		t.Errorf("cookie with insecure attributes: %+v", c)
	}

	// The session identifies the user.
	me := decode[auth.User](t, do(t, api, http.MethodGet, "/auth/me", "", c), http.StatusOK)
	if me.ID != u.ID {
		t.Errorf("me = %+v, want the user %d", me, u.ID)
	}

	// Logout: it deletes the cookie in the browser and the session in the database.
	w = do(t, api, http.MethodPost, "/auth/logout", "", c)
	if w.Code != http.StatusNoContent {
		t.Fatalf("logout status = %d", w.Code)
	}
	if cleared := sessionCookie(t, w); cleared.MaxAge >= 0 {
		t.Errorf("the cookie was not deleted: %+v", cleared)
	}
	if w := do(t, api, http.MethodGet, "/auth/me", "", c); w.Code != http.StatusUnauthorized {
		t.Errorf("the session is still active after the logout: status = %d", w.Code)
	}

	// Login with the email in a different letter case.
	w = do(t, api, http.MethodPost, "/auth/login", `{"email":"ALICE@example.com","password":"a-long-password"}`, nil)
	decode[auth.User](t, w, http.StatusOK)
	if w := do(t, api, http.MethodGet, "/auth/me", "", sessionCookie(t, w)); w.Code != http.StatusOK {
		t.Errorf("me after the login: status = %d", w.Code)
	}
}

func TestRegisterValidation(t *testing.T) {
	api, _ := newAPI(t)

	tests := []struct {
		name, body, field string
	}{
		{"without a name", `{"name":" ","email":"a@b.com","password":"a-long-password"}`, "name"},
		{"invalid email", `{"name":"Alice","email":"not-an-email","password":"a-long-password"}`, "email"},
		{"email with a name", `{"name":"Alice","email":"Alice <a@b.com>","password":"a-long-password"}`, "email"},
		{"email without a domain", `{"name":"Alice","email":"a@localhost","password":"a-long-password"}`, "email"},
		{"short password", `{"name":"Alice","email":"a@b.com","password":"1234567"}`, "password"},
		{"common password", `{"name":"Alice","email":"a@b.com","password":"Password1"}`, "password"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := decode[apiError](t, do(t, api, http.MethodPost, "/auth/register", tt.body, nil), http.StatusUnprocessableEntity)
			if resp.Error.Fields[tt.field] == "" {
				t.Errorf("want an error on %q: %+v", tt.field, resp.Error)
			}
		})
	}
}

func TestRegisterDuplicateEmail(t *testing.T) {
	api, _ := newAPI(t)

	decode[auth.User](t, do(t, api, http.MethodPost, "/auth/register", registerBody, nil), http.StatusCreated)

	dup := `{"name":"Another Alice","email":"alice@EXAMPLE.com","password":"another-long-password"}`
	resp := decode[apiError](t, do(t, api, http.MethodPost, "/auth/register", dup, nil), http.StatusConflict)
	if resp.Error.Code != "email_taken" || resp.Error.Fields["email"] == "" {
		t.Errorf("unexpected error: %+v", resp.Error)
	}
}

func TestLoginFailures(t *testing.T) {
	api, _ := newAPI(t)
	decode[auth.User](t, do(t, api, http.MethodPost, "/auth/register", registerBody, nil), http.StatusCreated)

	// A wrong password and a missing email answer exactly the same.
	wrongPass := do(t, api, http.MethodPost, "/auth/login", `{"email":"alice@example.com","password":"wrong-password"}`, nil)
	unknown := do(t, api, http.MethodPost, "/auth/login", `{"email":"nobody@example.com","password":"wrong-password"}`, nil)
	if wrongPass.Code != http.StatusUnauthorized || unknown.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d and %d, want 401", wrongPass.Code, unknown.Code)
	}
	if wrongPass.Body.String() != unknown.Body.String() {
		t.Errorf("the responses differ and reveal whether the email exists:\n%s\n%s", wrongPass.Body, unknown.Body)
	}

	resp := decode[apiError](t, do(t, api, http.MethodPost, "/auth/login", `{}`, nil), http.StatusUnprocessableEntity)
	if resp.Error.Fields["email"] == "" || resp.Error.Fields["password"] == "" {
		t.Errorf("unexpected error: %+v", resp.Error)
	}
}

func TestRequireUserRejectsInvalidSessions(t *testing.T) {
	api, _ := newAPI(t)

	for name, c := range map[string]*http.Cookie{
		"without a cookie": nil,
		"made-up token":    {Name: auth.CookieName, Value: "made-up-token"},
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
		t.Errorf("token in plain text: %d rows, hashed: %d rows", plain, hashed)
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
		u, err := service.CreateUser(t.Context(), auth.RegisterInput{Name: "x", Email: email, Password: "a-long-password"}, role)
		if err != nil {
			t.Fatalf("create user: %v", err)
		}
		w := httptest.NewRecorder()
		if err := service.StartSession(w, httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/auth/login", nil), u.ID); err != nil {
			t.Fatalf("start the session: %v", err)
		}
		return sessionCookie(t, w)
	}

	tests := []struct {
		name   string
		cookie *http.Cookie
		want   int
	}{
		{"without a session", nil, http.StatusUnauthorized},
		{"regular user", loginAs("user@example.com", auth.RoleUser), http.StatusForbidden},
		{"admin", loginAs("admin@example.com", auth.RoleAdmin), http.StatusOK},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if w := do(t, mux, http.MethodGet, "/admin", "", tt.cookie); w.Code != tt.want {
				t.Errorf("status = %d, want %d", w.Code, tt.want)
			}
		})
	}
}

func TestLogoutAlwaysClearsCookie(t *testing.T) {
	api, _ := newAPI(t)

	for name, c := range map[string]*http.Cookie{
		"without a cookie": nil,
		"expired session":  {Name: auth.CookieName, Value: "token-that-no-longer-exists"},
	} {
		t.Run(name, func(t *testing.T) {
			w := do(t, api, http.MethodPost, "/auth/logout", "", c)
			if w.Code != http.StatusNoContent {
				t.Fatalf("status = %d, want 204", w.Code)
			}
			if cleared := sessionCookie(t, w); cleared.MaxAge >= 0 {
				t.Errorf("the cookie was not deleted: %+v", cleared)
			}
		})
	}
}

// A password stored with old argon2 parameters (for example, before raising them) is hashed
// again with the current ones on sign in, and keeps working.
func TestLoginRehashesOldPassword(t *testing.T) {
	api, pool := newAPI(t)
	u, _ := testutil.Login(t, pool, "alice@example.com", auth.RoleUser)

	salt := []byte("sal-de-16-bytes!")
	key := argon2.IDKey([]byte(testutil.Password), salt, 1, 8*1024, 1, 32)
	old := fmt.Sprintf("$argon2id$v=%d$m=8192,t=1,p=1$%s$%s", argon2.Version,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key))
	if _, err := pool.Exec(t.Context(), "UPDATE users SET password_hash = $1 WHERE id = $2", old, u.ID); err != nil {
		t.Fatal(err)
	}

	if w := testutil.Do(t, api, nil, http.MethodPost, "/auth/login", loginBody); w.Code != http.StatusOK {
		t.Fatalf("login with the old hash: status = %d; body: %s", w.Code, w.Body)
	}
	var stored string
	if err := pool.QueryRow(t.Context(), "SELECT password_hash FROM users WHERE id = $1", u.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stored, "$m=19456,t=2,p=1$") {
		t.Errorf("the hash was not updated to the current parameters: %s", stored)
	}
	if w := testutil.Do(t, api, nil, http.MethodPost, "/auth/login", loginBody); w.Code != http.StatusOK {
		t.Errorf("login with the new hash: status = %d", w.Code)
	}
}

// The database requires a lowercase email: UNIQUE only prevents duplicates if nobody stores
// "Alice@Example.com" next to "alice@example.com", for example with a hand-written INSERT.
func TestEmailIsLowercaseInDatabase(t *testing.T) {
	_, pool := newAPI(t)
	_, err := pool.Exec(t.Context(), "INSERT INTO users (name, email, password_hash) VALUES ('Alice', 'Alice@Example.com', 'x')")
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23514" || pgErr.ConstraintName != "users_email_lowercase" {
		t.Fatalf("want the users_email_lowercase violation, got %v", err)
	}
}

// If the verification email cannot be queued, the account was still created: it answers 201,
// and the user can ask for another link.
func TestRegisterSucceedsWhenVerificationEmailFails(t *testing.T) {
	pool := testutil.NewDB(t)
	api := testutil.NewAPI(pool, testutil.WithMailer(failingMailer{}), testutil.WithConfig(requireVerification))

	w := testutil.Do(t, api, nil, http.MethodPost, "/auth/register", registerBody)
	u := testutil.Decode[auth.User](t, w, http.StatusCreated)
	if u.EmailVerified() {
		t.Error("the email should not be verified")
	}
	sessionCookie(t, w) // and the session stayed
}

type failingMailer struct{}

func (failingMailer) Send(context.Context, mail.Message) error { return errors.New("queue down") }
