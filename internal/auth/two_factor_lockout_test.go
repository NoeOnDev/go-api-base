package auth_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/NoeOnDev/go-api-base/internal/auth"
	"github.com/NoeOnDev/go-api-base/internal/mail"
	"github.com/NoeOnDev/go-api-base/internal/testutil"
)

// These tests simulate an attacker who has the password and spreads their attempts across many
// IPs: each request comes from a different IP, so the per-IP limit does not stop them.

func loginFromIP(t *testing.T, api http.Handler, ip string) *http.Cookie {
	t.Helper()
	w := testutil.DoFromIP(t, api, ip, nil, http.MethodPost, "/auth/login", loginBody)
	testutil.Decode[map[string]bool](t, w, http.StatusAccepted)
	for _, c := range w.Result().Cookies() {
		if c.Name == auth.ChallengeCookieName {
			return c
		}
	}
	t.Fatal("missing the challenge cookie")
	return nil
}

func challengeFromIP(t *testing.T, api http.Handler, ip string, cookie *http.Cookie, body string) *httptest.ResponseRecorder {
	t.Helper()
	return testutil.DoFromIP(t, api, ip, cookie, http.MethodPost, "/auth/two-factor/challenge", body)
}

const lockedSubject = "Sign-in attempt blocked"

func TestChallengeLocksAfterMaxAttempts(t *testing.T) {
	pool := testutil.NewDB(t)
	mails := &mail.Recorder{}
	api := testutil.NewAPI(pool, testutil.WithMailer(mails))
	_, session := testutil.Login(t, pool, "alice@example.com", auth.RoleUser)
	tf := testutil.EnableTwoFactor(t, api, session, testutil.Password)
	addr := &ips{}

	pending := loginFromIP(t, api, addr.next())

	// A malformed body does not spend attempts.
	testutil.Decode[testutil.APIError](t, challengeFromIP(t, api, addr.next(), pending, `{}`), http.StatusUnprocessableEntity)

	// The first 4 wrong codes allow retrying (one is a recovery code: they count the same).
	for i := range 4 {
		body := codeBody("000000")
		if i == 0 {
			body = `{"recovery_code":"aaaaa-bbbbb"}`
		}
		testutil.Decode[testutil.APIError](t, challengeFromIP(t, api, addr.next(), pending, body), http.StatusUnprocessableEntity)
	}

	// The fifth one ends the challenge.
	lastIP := addr.next()
	w := challengeFromIP(t, api, lastIP, pending, codeBody("000000"))
	resp := testutil.Decode[testutil.APIError](t, w, http.StatusUnauthorized)
	if resp.Error.Code != "two_factor_locked" {
		t.Errorf("want two_factor_locked: %+v", resp.Error)
	}
	for _, c := range w.Result().Cookies() {
		if c.Name == auth.ChallengeCookieName && c.MaxAge >= 0 {
			t.Error("the challenge cookie was not deleted")
		}
	}

	// The user gets the notice, with the IP of the last attempt.
	msg, ok := mails.Last("alice@example.com")
	if !ok || msg.Subject != lockedSubject || !strings.Contains(msg.Body, lastIP) || !strings.Contains(msg.Body, "https://app.test") {
		t.Errorf("unexpected notice (%v): %+v", ok, msg)
	}

	// That challenge no longer works, not even with the right code…
	resp = testutil.Decode[testutil.APIError](t, challengeFromIP(t, api, addr.next(), pending, codeBody(tf.Code(t))), http.StatusUnauthorized)
	if resp.Error.Code == "two_factor_locked" {
		t.Errorf("a deleted challenge should answer as expired: %+v", resp.Error)
	}

	// …but entering the password again, the right code works.
	if w := challengeFromIP(t, api, addr.next(), loginFromIP(t, api, addr.next()), codeBody(tf.Code(t))); w.Code != http.StatusOK {
		t.Errorf("new login: status = %d; body: %s", w.Code, w.Body)
	}
}

func TestChallengeUserLimitAcrossIPs(t *testing.T) {
	pool := testutil.NewDB(t)
	mails := &mail.Recorder{}
	api := testutil.NewAPI(pool, testutil.WithMailer(mails))
	_, session := testutil.Login(t, pool, "alice@example.com", auth.RoleUser)
	tf := testutil.EnableTwoFactor(t, api, session, testutil.Password)
	addr := &ips{}

	// Two used-up challenges: 10 wrong codes, each one from a different IP.
	for range 2 {
		pending := loginFromIP(t, api, addr.next())
		for range 5 {
			challengeFromIP(t, api, addr.next(), pending, codeBody("000000"))
		}
	}

	// With a new challenge and another IP, the next attempt is rejected, even with the right code.
	w := challengeFromIP(t, api, addr.next(), loginFromIP(t, api, addr.next()), codeBody(tf.Code(t)))
	resp := testutil.Decode[testutil.APIError](t, w, http.StatusTooManyRequests)
	if resp.Error.Code != "too_many_requests" || w.Header().Get("Retry-After") != "360" {
		t.Errorf("respuesta inesperada: %+v, Retry-After %q", resp.Error, w.Header().Get("Retry-After"))
	}

	// One notice per used-up challenge.
	sent := 0
	for _, m := range mails.Messages() {
		if m.Subject == lockedSubject {
			sent++
		}
	}
	if sent != 2 {
		t.Errorf("%d notices were sent, want 2", sent)
	}
}

func TestPasswordResetUnlocksTwoFactor(t *testing.T) {
	pool := testutil.NewDB(t)
	mails := &mail.Recorder{}
	api := testutil.NewAPI(pool, testutil.WithMailer(mails))
	_, session := testutil.Login(t, pool, "alice@example.com", auth.RoleUser)
	tf := testutil.EnableTwoFactor(t, api, session, testutil.Password)
	addr := &ips{}

	// Someone with the password uses up Alice's two-factor limit and leaves a pending challenge.
	for range 2 {
		pending := loginFromIP(t, api, addr.next())
		for range 5 {
			challengeFromIP(t, api, addr.next(), pending, codeBody("000000"))
		}
	}
	attacker := loginFromIP(t, api, addr.next())

	// Alice resets her password.
	testutil.DoFromIP(t, api, addr.next(), nil, http.MethodPost, "/auth/password/forgot", `{"email":"alice@example.com"}`)
	msg, _ := mails.Last("alice@example.com")
	if w := testutil.DoFromIP(t, api, addr.next(), nil, http.MethodPost, "/auth/password/reset", resetBody(testutil.TokenFromEmail(t, msg), newPassword)); w.Code != http.StatusNoContent {
		t.Fatalf("reset: status = %d", w.Code)
	}

	// The challenge obtained with the old password no longer works…
	testutil.Decode[testutil.APIError](t, challengeFromIP(t, api, addr.next(), attacker, codeBody(tf.Code(t))), http.StatusUnauthorized)

	// …and Alice signs in with the new password without waiting for the limit to refill.
	w := testutil.DoFromIP(t, api, addr.next(), nil, http.MethodPost, "/auth/login",
		fmt.Sprintf(`{"email":"alice@example.com","password":%q}`, newPassword))
	testutil.Decode[map[string]bool](t, w, http.StatusAccepted)
	var mine *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == auth.ChallengeCookieName {
			mine = c
		}
	}
	if w := challengeFromIP(t, api, addr.next(), mine, codeBody(tf.Code(t))); w.Code != http.StatusOK {
		t.Errorf("Alice's login after the reset: status = %d; body: %s", w.Code, w.Body)
	}
}

func TestPasswordChangeEndsPendingChallenges(t *testing.T) {
	pool := testutil.NewDB(t)
	api := testutil.NewAPI(pool)
	_, session := testutil.Login(t, pool, "alice@example.com", auth.RoleUser)
	tf := testutil.EnableTwoFactor(t, api, session, testutil.Password)
	addr := &ips{}

	// Someone with the password got through the first step of the login…
	attacker := loginFromIP(t, api, addr.next())

	// …and Alice, who has her session open, changes the password.
	body := fmt.Sprintf(`{"current_password":"test-password","password":%q}`, newPassword)
	if w := testutil.DoFromIP(t, api, addr.next(), session, http.MethodPut, "/auth/password", body); w.Code != http.StatusNoContent {
		t.Fatalf("password change: status = %d; body: %s", w.Code, w.Body)
	}

	testutil.Decode[testutil.APIError](t, challengeFromIP(t, api, addr.next(), attacker, codeBody(tf.Code(t))), http.StatusUnauthorized)
}
