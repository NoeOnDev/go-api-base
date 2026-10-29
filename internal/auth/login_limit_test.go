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

// Each attempt comes from a different IP: the per-IP limit does not step in, as in an attack
// spread across many IPs against the same account.

func loginBodyFor(email, password string) string {
	return fmt.Sprintf(`{"email":%q,"password":%q}`, email, password)
}

func TestLoginAccountLimit(t *testing.T) {
	pool := testutil.NewDB(t)
	mails := &mail.Recorder{}
	api := testutil.NewAPI(pool, testutil.WithMailer(mails))
	testutil.Login(t, pool, "alice@example.com", auth.RoleUser)
	addr := &ips{}

	// The same with a registered email and with one that does not exist: it does not reveal which is
	// which.
	for _, email := range []string{"alice@example.com", "nobody@example.com"} {
		for i := range 10 {
			w := testutil.DoFromIP(t, api, addr.next(), nil, http.MethodPost, "/auth/login", loginBodyFor(email, "wrong-password"))
			if w.Code != http.StatusUnauthorized {
				t.Fatalf("%s, attempt %d: status = %d", email, i+1, w.Code)
			}
		}
		// The eleventh one is rejected without checking the password, even if it is the right one.
		w := testutil.DoFromIP(t, api, addr.next(), nil, http.MethodPost, "/auth/login", loginBodyFor(" "+email, "test-password"))
		resp := testutil.Decode[testutil.APIError](t, w, http.StatusTooManyRequests)
		if resp.Error.Code != "too_many_requests" || w.Header().Get("Retry-After") != "60" {
			t.Errorf("%s: respuesta inesperada: %+v, Retry-After %q", email, resp.Error, w.Header().Get("Retry-After"))
		}
	}

	// Alice resets her password, and that resets the limit.
	testutil.DoFromIP(t, api, addr.next(), nil, http.MethodPost, "/auth/password/forgot", `{"email":"alice@example.com"}`)
	msg, _ := mails.Last("alice@example.com")
	if w := testutil.DoFromIP(t, api, addr.next(), nil, http.MethodPost, "/auth/password/reset", resetBody(testutil.TokenFromEmail(t, msg), newPassword)); w.Code != http.StatusNoContent {
		t.Fatalf("reset: status = %d", w.Code)
	}
	if w := testutil.DoFromIP(t, api, addr.next(), nil, http.MethodPost, "/auth/login", loginBodyFor("alice@example.com", newPassword)); w.Code != http.StatusOK {
		t.Errorf("login after the reset: status = %d; body: %s", w.Code, w.Body)
	}
}

func TestLoginRejectsLongEmail(t *testing.T) {
	pool := testutil.NewDB(t)
	api := testutil.NewAPI(pool)

	long := strings.Repeat("a", 300) + "@example.com"
	resp := testutil.Decode[testutil.APIError](t,
		testutil.Do(t, api, nil, http.MethodPost, "/auth/login", loginBodyFor(long, "test-password")), http.StatusUnprocessableEntity)
	if resp.Error.Fields["email"] == "" {
		t.Errorf("want an error on email: %+v", resp.Error)
	}
}
