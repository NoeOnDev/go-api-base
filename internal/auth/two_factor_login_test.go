package auth_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/NoeOnDev/go-api-base/internal/auth"
	"github.com/NoeOnDev/go-api-base/internal/config"
	"github.com/NoeOnDev/go-api-base/internal/mail"
	"github.com/NoeOnDev/go-api-base/internal/testutil"
)

// Careful: the login, enable and the challenge share the limit of 5 attempts per IP, so each
// test makes at most 5 requests to those routes.

// loginWithTwoFactor does the first step of the login and returns the challenge cookie.
func loginWithTwoFactor(t *testing.T, api http.Handler) *http.Cookie {
	t.Helper()
	w := testutil.Do(t, api, nil, http.MethodPost, "/auth/login", loginBody)
	resp := testutil.Decode[map[string]bool](t, w, http.StatusAccepted)
	if !resp["two_factor_required"] {
		t.Fatalf("want two_factor_required: %v", resp)
	}
	var challenge *http.Cookie
	for _, c := range w.Result().Cookies() {
		switch c.Name {
		case auth.CookieName:
			t.Fatal("it should not create the session before the second step")
		case auth.ChallengeCookieName:
			challenge = c
		}
	}
	if challenge == nil || !challenge.HttpOnly {
		t.Fatalf("missing the HttpOnly challenge cookie: %+v", challenge)
	}
	return challenge
}

func challenge(t *testing.T, api http.Handler, cookie *http.Cookie, body string) *httptest.ResponseRecorder {
	t.Helper()
	return testutil.Do(t, api, cookie, http.MethodPost, "/auth/two-factor/challenge", body)
}

func TestLoginRequiresSecondFactor(t *testing.T) {
	pool := testutil.NewDB(t)
	api := testutil.NewAPI(pool)
	_, session := testutil.Login(t, pool, "alice@example.com", auth.RoleUser)
	tf := testutil.EnableTwoFactor(t, api, session, testutil.Password)

	pending := loginWithTwoFactor(t, api)

	// A wrong code allows retrying (until the attempts of the challenge are used up).
	resp := testutil.Decode[testutil.APIError](t, challenge(t, api, pending, codeBody("000000")), http.StatusUnprocessableEntity)
	if resp.Error.Fields["code"] == "" {
		t.Errorf("want an error on code: %+v", resp.Error)
	}

	w := challenge(t, api, pending, codeBody(tf.Code(t)))
	u := testutil.Decode[auth.User](t, w, http.StatusOK)
	if u.Email != "alice@example.com" || !u.TwoFactorEnabled {
		t.Errorf("unexpected user: %+v", u)
	}

	// Now there is a session, and the challenge cookie is deleted.
	var newSession *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == auth.CookieName {
			newSession = c
		}
		if c.Name == auth.ChallengeCookieName && c.MaxAge >= 0 {
			t.Error("the challenge cookie was not deleted")
		}
	}
	if newSession == nil {
		t.Fatal("the session was not created")
	}
	if w := testutil.Do(t, api, newSession, http.MethodGet, "/auth/me", ""); w.Code != http.StatusOK {
		t.Errorf("the new session does not work: status = %d", w.Code)
	}
}

func TestTOTPCodeCannotBeReused(t *testing.T) {
	pool := testutil.NewDB(t)
	api := testutil.NewAPI(pool)
	_, session := testutil.Login(t, pool, "alice@example.com", auth.RoleUser)
	tf := testutil.EnableTwoFactor(t, api, session, testutil.Password)
	code := tf.Code(t)

	if w := challenge(t, api, loginWithTwoFactor(t, api), codeBody(code)); w.Code != http.StatusOK {
		t.Fatalf("primer uso: status = %d", w.Code)
	}

	// The same code, still within its time window, no longer works.
	testutil.Decode[testutil.APIError](t, challenge(t, api, loginWithTwoFactor(t, api), codeBody(code)), http.StatusUnprocessableEntity)
}

func TestRecoveryCodeLogin(t *testing.T) {
	pool := testutil.NewDB(t)
	api := testutil.NewAPI(pool)
	_, session := testutil.Login(t, pool, "alice@example.com", auth.RoleUser)
	tf := testutil.EnableTwoFactor(t, api, session, testutil.Password)

	// It is accepted however the user types it: in uppercase and without the hyphen.
	typed := strings.ToUpper(strings.ReplaceAll(tf.RecoveryCodes[0], "-", ""))
	body := fmt.Sprintf(`{"recovery_code":%q}`, typed)

	if w := challenge(t, api, loginWithTwoFactor(t, api), body); w.Code != http.StatusOK {
		t.Fatalf("recovery code: status = %d; body: %s", w.Code, w.Body)
	}

	// Each code works only once.
	resp := testutil.Decode[testutil.APIError](t, challenge(t, api, loginWithTwoFactor(t, api), body), http.StatusUnprocessableEntity)
	if resp.Error.Fields["recovery_code"] == "" {
		t.Errorf("want an error on recovery_code: %+v", resp.Error)
	}
}

func TestTwoFactorChallengeErrors(t *testing.T) {
	pool := testutil.NewDB(t)
	api := testutil.NewAPI(pool)
	_, session := testutil.Login(t, pool, "alice@example.com", auth.RoleUser)
	tf := testutil.EnableTwoFactor(t, api, session, testutil.Password)

	// Without the challenge cookie (it did not go through the login).
	testutil.Decode[testutil.APIError](t, challenge(t, api, nil, codeBody(tf.Code(t))), http.StatusUnauthorized)

	pending := loginWithTwoFactor(t, api)

	// One of the two codes has to be sent, not both.
	both := fmt.Sprintf(`{"code":%q,"recovery_code":%q}`, tf.Code(t), tf.RecoveryCodes[0])
	testutil.Decode[testutil.APIError](t, challenge(t, api, pending, both), http.StatusUnprocessableEntity)

	// Expired challenge.
	if _, err := pool.Exec(t.Context(), "UPDATE user_tokens SET expires_at = now() - interval '1 minute'"); err != nil {
		t.Fatal(err)
	}
	testutil.Decode[testutil.APIError](t, challenge(t, api, pending, codeBody(tf.Code(t))), http.StatusUnauthorized)
}

func TestPasswordResetDoesNotBypassTwoFactor(t *testing.T) {
	pool := testutil.NewDB(t)
	mails := &mail.Recorder{}
	api := testutil.NewAPI(pool, testutil.WithMailer(mails))
	_, session := testutil.Login(t, pool, "alice@example.com", auth.RoleUser)
	testutil.EnableTwoFactor(t, api, session, testutil.Password)

	testutil.Do(t, api, nil, http.MethodPost, "/auth/password/forgot", `{"email":"alice@example.com"}`)
	msg, _ := mails.Last("alice@example.com")
	reset := fmt.Sprintf(`{"token":%q,"password":"test-password"}`, testutil.TokenFromEmail(t, msg))
	if w := testutil.Do(t, api, nil, http.MethodPost, "/auth/password/reset", reset); w.Code != http.StatusNoContent {
		t.Fatalf("reset: status = %d", w.Code)
	}

	// After a password reset, the login still asks for the second factor.
	loginWithTwoFactor(t, api)
}

// The second step of the login has its own limit: it does not spend the login's attempts. With
// the shared limit of before (5 per IP), this test stopped at the third login.
func TestTwoFactorChallengeHasItsOwnLimit(t *testing.T) {
	pool := testutil.NewDB(t)
	api := testutil.NewAPI(pool)
	_, session := testutil.Login(t, pool, "alice@example.com", auth.RoleUser)
	testutil.EnableTwoFactor(t, api, session, testutil.Password)

	// 10 logins with their second step (with a wrong code), all from the same IP.
	for i := range 10 {
		pending := loginWithTwoFactor(t, api) // it fails the test if the login does not answer 202
		if w := challenge(t, api, pending, codeBody("000000")); w.Code != http.StatusUnprocessableEntity {
			t.Fatalf("two-factor attempt %d: status = %d, want 422", i+1, w.Code)
		}
	}
}

// In production (COOKIE_SECURE=true) the challenge cookie also has the __Host- prefix.
func TestChallengeCookieUsesHostPrefix(t *testing.T) {
	pool := testutil.NewDB(t)
	secure := testutil.WithConfig(func(c *config.Config) { c.CookieSecure = true })
	api := testutil.NewAPI(pool, secure)
	_, session := testutil.Login(t, pool, "alice@example.com", auth.RoleUser, secure)
	tf := testutil.EnableTwoFactor(t, api, session, testutil.Password)

	w := testutil.Do(t, api, nil, http.MethodPost, "/auth/login", loginBody)
	if w.Code != http.StatusAccepted {
		t.Fatalf("login: status = %d", w.Code)
	}
	pending := cookieNamed(t, w, "__Host-two_factor_challenge")
	if !pending.Secure || !pending.HttpOnly || pending.Path != "/" || pending.Domain != "" {
		t.Errorf("the challenge cookie breaks the __Host- rules: %+v", pending)
	}
	w = challenge(t, api, pending, codeBody(tf.Code(t)))
	if w.Code != http.StatusOK {
		t.Fatalf("challenge: status = %d; body: %s", w.Code, w.Body)
	}
	cookieNamed(t, w, "__Host-session")
}
