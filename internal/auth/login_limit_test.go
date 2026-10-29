package auth_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/NoeOnDev/go-api-base/internal/auth"
	"github.com/NoeOnDev/go-api-base/internal/mail"
	"github.com/NoeOnDev/go-api-base/internal/testutil"
)

// Cada intento sale de una IP distinta: el límite por IP no interviene, como en un ataque
// repartido entre muchas IPs contra una misma cuenta.

func loginBodyFor(email, password string) string {
	return fmt.Sprintf(`{"email":%q,"password":%q}`, email, password)
}

func TestLoginAccountLimit(t *testing.T) {
	pool := testutil.NewDB(t)
	mails := &mail.Recorder{}
	api := testutil.NewAPI(pool, testutil.WithMailer(mails))
	testutil.Login(t, pool, "alice@example.com", auth.RoleUser)
	addr := &ips{}

	// Igual con un email registrado y con uno que no existe: no revela cuál es cuál.
	for _, email := range []string{"alice@example.com", "nobody@example.com"} {
		for i := range 10 {
			w := testutil.DoFromIP(t, api, addr.next(), nil, http.MethodPost, "/auth/login", loginBodyFor(email, "contraseña-incorrecta"))
			if w.Code != http.StatusUnauthorized {
				t.Fatalf("%s, intento %d: status = %d", email, i+1, w.Code)
			}
		}
		// El undécimo se rechaza sin evaluar la contraseña, aunque sea la correcta.
		w := testutil.DoFromIP(t, api, addr.next(), nil, http.MethodPost, "/auth/login", loginBodyFor(" "+email, "test-password"))
		resp := testutil.Decode[testutil.APIError](t, w, http.StatusTooManyRequests)
		if resp.Error.Code != "too_many_requests" || w.Header().Get("Retry-After") != "60" {
			t.Errorf("%s: respuesta inesperada: %+v, Retry-After %q", email, resp.Error, w.Header().Get("Retry-After"))
		}
	}

	// Alice recupera su contraseña, y eso le reinicia el límite.
	testutil.DoFromIP(t, api, addr.next(), nil, http.MethodPost, "/auth/password/forgot", `{"email":"alice@example.com"}`)
	msg, _ := mails.Last("alice@example.com")
	if w := testutil.DoFromIP(t, api, addr.next(), nil, http.MethodPost, "/auth/password/reset", resetBody(testutil.TokenFromEmail(t, msg), newPassword)); w.Code != http.StatusNoContent {
		t.Fatalf("reset: status = %d", w.Code)
	}
	if w := testutil.DoFromIP(t, api, addr.next(), nil, http.MethodPost, "/auth/login", loginBodyFor("alice@example.com", newPassword)); w.Code != http.StatusOK {
		t.Errorf("login después del reset: status = %d; cuerpo: %s", w.Code, w.Body)
	}
}

func TestLoginRejectsLongEmail(t *testing.T) {
	pool := testutil.NewDB(t)
	api := testutil.NewAPI(pool)

	long := strings.Repeat("a", 300) + "@example.com"
	resp := testutil.Decode[testutil.APIError](t,
		testutil.Do(t, api, nil, http.MethodPost, "/auth/login", loginBodyFor(long, "test-password")), http.StatusUnprocessableEntity)
	if resp.Error.Fields["email"] == "" {
		t.Errorf("se esperaba error en email: %+v", resp.Error)
	}
}
