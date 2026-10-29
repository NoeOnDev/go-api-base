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
	t.Fatalf("missing the cookie %s; got: %v", name, w.Result().Cookies())
	return nil
}

// In production (COOKIE_SECURE=true) cookies have the __Host- prefix, which the browser only
// accepts if they are Secure, have Path=/ and have no Domain.
func TestSecureCookiesUseHostPrefix(t *testing.T) {
	pool := testutil.NewDB(t)
	api := testutil.NewAPI(pool, testutil.WithConfig(func(c *config.Config) { c.CookieSecure = true }))

	w := testutil.Do(t, api, nil, http.MethodPost, "/auth/register", registerBody)
	if w.Code != http.StatusCreated {
		t.Fatalf("register: status = %d", w.Code)
	}
	session := cookieNamed(t, w, "__Host-session")
	if !session.Secure || !session.HttpOnly || session.Path != "/" || session.Domain != "" || session.SameSite != http.SameSiteLaxMode {
		t.Errorf("the cookie breaks the __Host- rules: %+v", session)
	}

	if w := testutil.Do(t, api, session, http.MethodGet, "/auth/me", ""); w.Code != http.StatusOK {
		t.Errorf("/auth/me with the cookie: status = %d", w.Code)
	}
	// A cookie with the name without the prefix (for example, created by a subdomain) does not work.
	plain := &http.Cookie{Name: "session", Value: session.Value}
	if w := testutil.Do(t, api, plain, http.MethodGet, "/auth/me", ""); w.Code != http.StatusUnauthorized {
		t.Errorf("/auth/me with the cookie without the prefix: status = %d", w.Code)
	}

}

func TestLoginHelperWithSecureCookies(t *testing.T) {
	pool := testutil.NewDB(t)
	secure := testutil.WithConfig(func(c *config.Config) { c.CookieSecure = true })
	api := testutil.NewAPI(pool, secure)

	// With the same options as the API, the testutil.Login cookie matches.
	_, cookie := testutil.Login(t, pool, "alice@example.com", "user", secure)
	if cookie.Name != "__Host-session" {
		t.Errorf("cookie = %q, want __Host-session", cookie.Name)
	}
	if w := testutil.Do(t, api, cookie, http.MethodGet, "/auth/me", ""); w.Code != http.StatusOK {
		t.Errorf("/auth/me: status = %d", w.Code)
	}
}
