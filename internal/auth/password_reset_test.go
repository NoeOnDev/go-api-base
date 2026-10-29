package auth_test

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/NoeOnDev/go-api-base/internal/auth"
	"github.com/NoeOnDev/go-api-base/internal/mail"
	"github.com/NoeOnDev/go-api-base/internal/testutil"
)

func TestForgotPasswordDoesNotRevealEmails(t *testing.T) {
	pool := testutil.NewDB(t)
	mails := &mail.Recorder{}
	api := testutil.NewAPI(pool, testutil.WithMailer(mails))
	testutil.Login(t, pool, "alice@example.com", auth.RoleUser)

	unknown := testutil.Do(t, api, nil, http.MethodPost, "/auth/password/forgot", `{"email":"nobody@example.com"}`)
	known := testutil.Do(t, api, nil, http.MethodPost, "/auth/password/forgot", `{"email":" ALICE@example.com "}`)

	if unknown.Code != http.StatusAccepted || known.Code != http.StatusAccepted {
		t.Fatalf("status = %d and %d, want 202", unknown.Code, known.Code)
	}
	if unknown.Body.String() != known.Body.String() {
		t.Errorf("the responses differ:\n%s\n%s", unknown.Body, known.Body)
	}

	// Only one email was sent, to the registered address, with the link to the frontend.
	msgs := mails.Messages()
	if len(msgs) != 1 || msgs[0].To != "alice@example.com" {
		t.Fatalf("emails sent: %+v", msgs)
	}
	if !strings.Contains(msgs[0].Body, "https://app.test/reset-password?token=") {
		t.Errorf("the email does not have the expected link:\n%s", msgs[0].Body)
	}

	resp := testutil.Decode[testutil.APIError](t,
		testutil.Do(t, api, nil, http.MethodPost, "/auth/password/forgot", `{"email":"no-es-email"}`),
		http.StatusUnprocessableEntity)
	if resp.Error.Fields["email"] == "" {
		t.Errorf("want an error on email: %+v", resp.Error)
	}
}

func TestResetPassword(t *testing.T) {
	pool := testutil.NewDB(t)
	mails := &mail.Recorder{}
	api := testutil.NewAPI(pool, testutil.WithMailer(mails))
	user, session := testutil.Login(t, pool, "alice@example.com", auth.RoleUser)

	testutil.Do(t, api, nil, http.MethodPost, "/auth/password/forgot", `{"email":"alice@example.com"}`)
	msg, _ := mails.Last("alice@example.com")
	token := testutil.TokenFromEmail(t, msg)

	t.Run("an invalid password does not use up the token", func(t *testing.T) {
		resp := testutil.Decode[testutil.APIError](t,
			testutil.Do(t, api, nil, http.MethodPost, "/auth/password/reset", resetBody(token, "short")),
			http.StatusUnprocessableEntity)
		if resp.Error.Fields["password"] == "" {
			t.Errorf("want an error on password: %+v", resp.Error)
		}
	})

	if w := testutil.Do(t, api, nil, http.MethodPost, "/auth/password/reset", resetBody(token, newPassword)); w.Code != http.StatusNoContent {
		t.Fatalf("reset: status = %d; body: %s", w.Code, w.Body)
	}

	// Every session of the user was closed.
	if w := testutil.Do(t, api, session, http.MethodGet, "/auth/me", ""); w.Code != http.StatusUnauthorized {
		t.Errorf("the previous session is still active: status = %d", w.Code)
	}

	// The password changed and the email was verified (the user received the email).
	service := auth.NewService(pool, auth.Options{})
	u, err := service.Authenticate(t.Context(), "alice@example.com", newPassword)
	if err != nil {
		t.Fatalf("login with the new password: %v", err)
	}
	if !u.EmailVerified() || u.ID != user.ID {
		t.Errorf("unexpected user: %+v", u)
	}

	// The token works only once.
	resp := testutil.Decode[testutil.APIError](t,
		testutil.Do(t, api, nil, http.MethodPost, "/auth/password/reset", resetBody(token, "another-long-password")),
		http.StatusUnprocessableEntity)
	if resp.Error.Fields["token"] == "" {
		t.Errorf("want an error on token: %+v", resp.Error)
	}
}

func TestResetPasswordInvalidTokens(t *testing.T) {
	pool := testutil.NewDB(t)
	mails := &mail.Recorder{}
	api := testutil.NewAPI(pool, testutil.WithMailer(mails))
	testutil.Login(t, pool, "alice@example.com", auth.RoleUser)

	forgot := func() string {
		testutil.Do(t, api, nil, http.MethodPost, "/auth/password/forgot", `{"email":"alice@example.com"}`)
		msg, _ := mails.Last("alice@example.com")
		return testutil.TokenFromEmail(t, msg)
	}

	first := forgot()
	second := forgot() // asking for another link invalidates the previous one

	expired := second
	if _, err := pool.Exec(t.Context(), "UPDATE user_tokens SET expires_at = now() - interval '1 minute'"); err != nil {
		t.Fatal(err)
	}

	for name, token := range map[string]string{
		"made up":                 "made-up-token",
		"empty":                   "",
		"replaced by another one": first,
		"expired":                 expired,
	} {
		t.Run(name, func(t *testing.T) {
			testutil.Decode[testutil.APIError](t,
				testutil.Do(t, api, nil, http.MethodPost, "/auth/password/reset", resetBody(token, newPassword)),
				http.StatusUnprocessableEntity)
		})
	}
}

// If a step of the reset fails, nothing changes and the link still works.
func TestResetPasswordIsAtomic(t *testing.T) {
	pool := testutil.NewDB(t)
	mails := &mail.Recorder{}
	api := testutil.NewAPI(pool, testutil.WithMailer(mails))
	testutil.Login(t, pool, "alice@example.com", auth.RoleUser) // a session to close

	testutil.Do(t, api, nil, http.MethodPost, "/auth/password/forgot", `{"email":"alice@example.com"}`)
	msg, _ := mails.Last("alice@example.com")
	body := resetBody(testutil.TokenFromEmail(t, msg), newPassword)

	exec := func(sql string) error { _, err := pool.Exec(context.Background(), sql); return err }
	failSessionDeletes(t, exec)

	// Closing the sessions fails, the last step: everything is undone.
	testutil.Decode[testutil.APIError](t, testutil.Do(t, api, nil, http.MethodPost, "/auth/password/reset", body), http.StatusInternalServerError)

	service := auth.NewService(pool, auth.Options{})
	if _, err := service.Authenticate(t.Context(), "alice@example.com", "test-password"); err != nil {
		t.Errorf("the password should not have changed: %v", err)
	}

	// Without the failure, the same link works: it was not used up.
	if err := exec("DROP TRIGGER test_fail_session_delete ON sessions"); err != nil {
		t.Fatal(err)
	}
	if w := testutil.Do(t, api, nil, http.MethodPost, "/auth/password/reset", body); w.Code != http.StatusNoContent {
		t.Fatalf("the link should still work: status = %d; body: %s", w.Code, w.Body)
	}
	if _, err := service.Authenticate(t.Context(), "alice@example.com", newPassword); err != nil {
		t.Errorf("the new password should work: %v", err)
	}
}

// Several simultaneous link requests for the same user leave a single valid link.
// (Without the lock on the user's row, this test would fail or not depending on the timing;
// with the lock it always passes.)
func TestConcurrentForgotLeavesOneToken(t *testing.T) {
	pool := testutil.NewDB(t)
	testutil.Login(t, pool, "alice@example.com", auth.RoleUser)

	for round := range 5 {
		api := testutil.NewAPI(pool) // new limits in each round
		var wg sync.WaitGroup
		for i := range 3 {
			wg.Go(func() {
				ip := fmt.Sprintf("198.51.%d.%d", round, i+1)
				testutil.DoFromIP(t, api, ip, nil, http.MethodPost, "/auth/password/forgot", `{"email":"alice@example.com"}`)
			})
		}
		wg.Wait()

		var n int
		if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM user_tokens WHERE purpose = 'password_reset'").Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 1 {
			t.Fatalf("round %d: %d valid links left, want 1", round+1, n)
		}
	}
}

// The per-recipient limits stop whoever requests emails for someone else from many IPs: each
// request comes from a different IP, so the per-IP limit does not step in.
func TestForgotPasswordRecipientLimit(t *testing.T) {
	pool := testutil.NewDB(t)
	mails := &mail.Recorder{}
	api := testutil.NewAPI(pool, testutil.WithMailer(mails))
	testutil.Login(t, pool, "alice@example.com", auth.RoleUser)

	for i := range 4 {
		ip := fmt.Sprintf("198.51.100.%d", i+1)
		w := testutil.DoFromIP(t, api, ip, nil, http.MethodPost, "/auth/password/forgot", `{"email":"alice@example.com"}`)
		// Always the same answer, so nothing is revealed.
		testutil.Decode[map[string]string](t, w, http.StatusAccepted)
	}

	if n := len(mails.Messages()); n != 3 {
		t.Fatalf("%d emails were sent, want 3", n)
	}

	// The fourth request did not invalidate the last link sent.
	last, _ := mails.Last("alice@example.com")
	w := testutil.Do(t, api, nil, http.MethodPost, "/auth/password/reset", resetBody(testutil.TokenFromEmail(t, last), newPassword))
	if w.Code != http.StatusNoContent {
		t.Errorf("the last link should still work: status = %d; body: %s", w.Code, w.Body)
	}
}
