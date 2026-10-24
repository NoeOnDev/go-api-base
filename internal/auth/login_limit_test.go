package auth_test

import (
	"fmt"
	"net/http"
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
	testutil.Login(t, pool, "ana@ejemplo.com", auth.RoleUser)
	addr := &ips{}

	// Igual con un email registrado y con uno que no existe: no revela cuál es cuál.
	for _, email := range []string{"ana@ejemplo.com", "nadie@ejemplo.com"} {
		for i := range 10 {
			w := testutil.DoFromIP(t, api, addr.next(), nil, http.MethodPost, "/auth/login", loginBodyFor(email, "contraseña-incorrecta"))
			if w.Code != http.StatusUnauthorized {
				t.Fatalf("%s, intento %d: status = %d", email, i+1, w.Code)
			}
		}
		// El undécimo se rechaza sin evaluar la contraseña, aunque sea la correcta.
		w := testutil.DoFromIP(t, api, addr.next(), nil, http.MethodPost, "/auth/login", loginBodyFor(" "+email, "contraseña-de-prueba"))
		resp := testutil.Decode[testutil.APIError](t, w, http.StatusTooManyRequests)
		if resp.Error.Code != "too_many_requests" || w.Header().Get("Retry-After") != "60" {
			t.Errorf("%s: respuesta inesperada: %+v, Retry-After %q", email, resp.Error, w.Header().Get("Retry-After"))
		}
	}

	// Ana recupera su contraseña, y eso le reinicia el límite.
	testutil.DoFromIP(t, api, addr.next(), nil, http.MethodPost, "/auth/password/forgot", `{"email":"ana@ejemplo.com"}`)
	msg, _ := mails.Last("ana@ejemplo.com")
	if w := testutil.DoFromIP(t, api, addr.next(), nil, http.MethodPost, "/auth/password/reset", resetBody(testutil.TokenFromEmail(t, msg), newPassword)); w.Code != http.StatusNoContent {
		t.Fatalf("reset: status = %d", w.Code)
	}
	if w := testutil.DoFromIP(t, api, addr.next(), nil, http.MethodPost, "/auth/login", loginBodyFor("ana@ejemplo.com", newPassword)); w.Code != http.StatusOK {
		t.Errorf("login después del reset: status = %d; cuerpo: %s", w.Code, w.Body)
	}
}
