package auth_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/NoeOnDev/go-api-base/internal/config"
	"github.com/NoeOnDev/go-api-base/internal/testutil"
)

func cookieNamed(t *testing.T, w *httptest.ResponseRecorder, name string) *http.Cookie {
	t.Helper()
	for _, c := range w.Result().Cookies() {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("falta la cookie %s; llegaron: %v", name, w.Result().Cookies())
	return nil
}

// En producción (COOKIE_SECURE=true) las cookies llevan el prefijo __Host-, que el navegador
// solo acepta si son Secure, tienen Path=/ y no tienen Domain.
func TestSecureCookiesUseHostPrefix(t *testing.T) {
	pool := testutil.NewDB(t)
	api := testutil.NewAPI(pool, testutil.WithConfig(func(c *config.Config) { c.CookieSecure = true }))

	w := testutil.Do(t, api, nil, http.MethodPost, "/auth/register", registerBody)
	if w.Code != http.StatusCreated {
		t.Fatalf("registro: status = %d", w.Code)
	}
	session := cookieNamed(t, w, "__Host-session")
	if !session.Secure || !session.HttpOnly || session.Path != "/" || session.Domain != "" || session.SameSite != http.SameSiteLaxMode {
		t.Errorf("la cookie no cumple las reglas de __Host-: %+v", session)
	}

	if w := testutil.Do(t, api, session, http.MethodGet, "/auth/me", ""); w.Code != http.StatusOK {
		t.Errorf("/auth/me con la cookie: status = %d", w.Code)
	}
	// Una cookie con el nombre sin prefijo (por ejemplo, creada por un subdominio) no sirve.
	plain := &http.Cookie{Name: "session", Value: session.Value}
	if w := testutil.Do(t, api, plain, http.MethodGet, "/auth/me", ""); w.Code != http.StatusUnauthorized {
		t.Errorf("/auth/me con la cookie sin prefijo: status = %d", w.Code)
	}

}

func TestLoginHelperWithSecureCookies(t *testing.T) {
	pool := testutil.NewDB(t)
	secure := testutil.WithConfig(func(c *config.Config) { c.CookieSecure = true })
	api := testutil.NewAPI(pool, secure)

	// Con las mismas opciones que la API, la cookie de testutil.Login coincide.
	_, cookie := testutil.Login(t, pool, "alice@example.com", "user", secure)
	if cookie.Name != "__Host-session" {
		t.Errorf("cookie = %q, se esperaba __Host-session", cookie.Name)
	}
	if w := testutil.Do(t, api, cookie, http.MethodGet, "/auth/me", ""); w.Code != http.StatusOK {
		t.Errorf("/auth/me: status = %d", w.Code)
	}
}
