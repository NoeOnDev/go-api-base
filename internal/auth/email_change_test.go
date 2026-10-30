package auth_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/NoeOnDev/go-api-base/internal/auth"
	"github.com/NoeOnDev/go-api-base/internal/mail"
	"github.com/NoeOnDev/go-api-base/internal/testutil"
)

const changeToNew = `{"email":"Alice.New@Example.com","password":"test-password"}`

func confirmBody(token string) string { return `{"token":"` + token + `"}` }

func TestChangeEmail(t *testing.T) {
	pool := testutil.NewDB(t)
	mails := &mail.Recorder{}
	api := testutil.NewAPI(pool, testutil.WithMailer(mails))
	_, cookie := testutil.Login(t, pool, "alice@example.com", auth.RoleUser)

	if w := testutil.Do(t, api, cookie, http.MethodPut, "/auth/email", changeToNew); w.Code != http.StatusAccepted {
		t.Fatalf("request the change: status = %d; body: %s", w.Code, w.Body)
	}

	// The link goes to the new email; the notice, to the current one.
	link, ok := mails.Last("alice.new@example.com")
	if !ok || !strings.Contains(link.Body, "https://app.test/confirm-email?token=") {
		t.Fatalf("the link did not reach the new email: %+v", mails.Messages())
	}
	notice, ok := mails.Last("alice@example.com")
	if !ok || !strings.Contains(notice.Body, "alice.new@example.com") || strings.Contains(notice.Body, "token=") {
		t.Fatalf("the notice to the current email: %+v", notice)
	}

	// Until it is confirmed, the email does not change.
	if u := testutil.Decode[auth.User](t, testutil.Do(t, api, cookie, http.MethodGet, "/auth/me", ""), http.StatusOK); u.Email != "alice@example.com" {
		t.Fatalf("the email changed before the confirmation: %s", u.Email)
	}

	token := testutil.TokenFromEmail(t, link)
	if w := testutil.Do(t, api, nil, http.MethodPost, "/auth/email/confirm", confirmBody(token)); w.Code != http.StatusNoContent {
		t.Fatalf("confirm: status = %d; body: %s", w.Code, w.Body)
	}
	u := testutil.Decode[auth.User](t, testutil.Do(t, api, cookie, http.MethodGet, "/auth/me", ""), http.StatusOK)
	if u.Email != "alice.new@example.com" || !u.EmailVerified() {
		t.Errorf("after the confirmation: %+v", u)
	}

	// The login uses the new email, and the link works only once.
	service := auth.NewService(pool, auth.Options{})
	if _, err := service.Authenticate(t.Context(), "alice.new@example.com", "test-password"); err != nil {
		t.Errorf("login with the new email: %v", err)
	}
	if _, err := service.Authenticate(t.Context(), "alice@example.com", "test-password"); err == nil {
		t.Error("the old email should no longer work")
	}
	testutil.Decode[testutil.APIError](t, testutil.Do(t, api, nil, http.MethodPost, "/auth/email/confirm", confirmBody(token)), http.StatusUnprocessableEntity)

	// And the notification stays.
	var kind string
	if err := pool.QueryRow(t.Context(), "SELECT kind FROM notifications ORDER BY id DESC LIMIT 1").Scan(&kind); err != nil || kind != auth.NotifyEmailChanged {
		t.Errorf("notification: %q (%v)", kind, err)
	}
}

func TestChangeEmailRejects(t *testing.T) {
	pool := testutil.NewDB(t)
	api := testutil.NewAPI(pool)
	_, cookie := testutil.Login(t, pool, "alice@example.com", auth.RoleUser)
	testutil.Login(t, pool, "bob@example.com", auth.RoleUser)

	for name, tt := range map[string]struct {
		body   string
		status int
	}{
		"without a password":       {`{"email":"new@example.com"}`, http.StatusUnprocessableEntity},
		"wrong password":           {`{"email":"new@example.com","password":"other"}`, http.StatusUnprocessableEntity},
		"invalid email":            {`{"email":"not-an-email","password":"test-password"}`, http.StatusUnprocessableEntity},
		"the same email":           {`{"email":"ALICE@example.com","password":"test-password"}`, http.StatusUnprocessableEntity},
		"email of another account": {`{"email":"bob@example.com","password":"test-password"}`, http.StatusConflict},
	} {
		if w := testutil.Do(t, api, cookie, http.MethodPut, "/auth/email", tt.body); w.Code != tt.status {
			t.Errorf("%s: status = %d, want %d", name, w.Code, tt.status)
		}
	}
	testutil.Decode[testutil.APIError](t, testutil.Do(t, api, nil, http.MethodPut, "/auth/email", changeToNew), http.StatusUnauthorized)
}

// If another account took the new email in the meantime, the confirmation answers 409 and
// changes nothing.
func TestConfirmEmailTakenMeanwhile(t *testing.T) {
	pool := testutil.NewDB(t)
	mails := &mail.Recorder{}
	api := testutil.NewAPI(pool, testutil.WithMailer(mails))
	_, cookie := testutil.Login(t, pool, "alice@example.com", auth.RoleUser)

	testutil.Do(t, api, cookie, http.MethodPut, "/auth/email", changeToNew)
	link, _ := mails.Last("alice.new@example.com")

	testutil.Login(t, pool, "alice.new@example.com", auth.RoleUser) // another account registers with that email

	testutil.Decode[testutil.APIError](t, testutil.Do(t, api, nil, http.MethodPost, "/auth/email/confirm", confirmBody(testutil.TokenFromEmail(t, link))), http.StatusConflict)
	if u := testutil.Decode[auth.User](t, testutil.Do(t, api, cookie, http.MethodGet, "/auth/me", ""), http.StatusOK); u.Email != "alice@example.com" {
		t.Errorf("the email should not have changed: %s", u.Email)
	}
}

// Confirming the new email cancels the reset links sent to the previous one: if the user changed
// the email because they lost control of the old one, whoever has it cannot use them.
func TestConfirmEmailChangeRevokesPasswordReset(t *testing.T) {
	pool := testutil.NewDB(t)
	mails := &mail.Recorder{}
	api := testutil.NewAPI(pool, testutil.WithMailer(mails))
	_, cookie := testutil.Login(t, pool, "alice@example.com", auth.RoleUser)

	testutil.Do(t, api, nil, http.MethodPost, "/auth/password/forgot", `{"email":"alice@example.com"}`)
	reset, ok := mails.Last("alice@example.com")
	if !ok {
		t.Fatal("the reset link did not arrive")
	}
	resetToken := testutil.TokenFromEmail(t, reset)

	testutil.Do(t, api, cookie, http.MethodPut, "/auth/email", changeToNew)
	link, _ := mails.Last("alice.new@example.com")
	if w := testutil.Do(t, api, nil, http.MethodPost, "/auth/email/confirm", confirmBody(testutil.TokenFromEmail(t, link))); w.Code != http.StatusNoContent {
		t.Fatalf("confirm: status = %d; body: %s", w.Code, w.Body)
	}

	testutil.Decode[testutil.APIError](t,
		testutil.Do(t, api, nil, http.MethodPost, "/auth/password/reset", resetBody(resetToken, newPassword)),
		http.StatusUnprocessableEntity)
	service := auth.NewService(pool, auth.Options{})
	if _, err := service.Authenticate(t.Context(), "alice.new@example.com", "test-password"); err != nil {
		t.Errorf("the password should not have changed: %v", err)
	}
}

// Changing or resetting the password cancels a pending email change: whoever knew the previous
// password could have requested it, to keep the account.
func TestPasswordChangeCancelsEmailChange(t *testing.T) {
	// requestChange requests the email change and returns the token of the link.
	requestChange := func(t *testing.T, api http.Handler, mails *mail.Recorder, cookie *http.Cookie) string {
		t.Helper()
		if w := testutil.Do(t, api, cookie, http.MethodPut, "/auth/email", changeToNew); w.Code != http.StatusAccepted {
			t.Fatalf("request the change: status = %d; body: %s", w.Code, w.Body)
		}
		link, _ := mails.Last("alice.new@example.com")
		return testutil.TokenFromEmail(t, link)
	}

	t.Run("when changing the password", func(t *testing.T) {
		pool := testutil.NewDB(t)
		mails := &mail.Recorder{}
		api := testutil.NewAPI(pool, testutil.WithMailer(mails))
		_, cookie := testutil.Login(t, pool, "alice@example.com", auth.RoleUser)

		token := requestChange(t, api, mails, cookie)
		body := `{"current_password":"test-password","password":"` + newPassword + `"}`
		if w := testutil.Do(t, api, cookie, http.MethodPut, "/auth/password", body); w.Code != http.StatusNoContent {
			t.Fatalf("change the password: status = %d; body: %s", w.Code, w.Body)
		}

		testutil.Decode[testutil.APIError](t, testutil.Do(t, api, nil, http.MethodPost, "/auth/email/confirm", confirmBody(token)), http.StatusUnprocessableEntity)
		if u := testutil.Decode[auth.User](t, testutil.Do(t, api, cookie, http.MethodGet, "/auth/me", ""), http.StatusOK); u.Email != "alice@example.com" {
			t.Errorf("the email should not have changed: %s", u.Email)
		}
	})

	t.Run("when resetting the password", func(t *testing.T) {
		pool := testutil.NewDB(t)
		mails := &mail.Recorder{}
		api := testutil.NewAPI(pool, testutil.WithMailer(mails))
		_, cookie := testutil.Login(t, pool, "alice@example.com", auth.RoleUser)

		token := requestChange(t, api, mails, cookie)
		testutil.Do(t, api, nil, http.MethodPost, "/auth/password/forgot", `{"email":"alice@example.com"}`)
		reset, _ := mails.Last("alice@example.com")
		if w := testutil.Do(t, api, nil, http.MethodPost, "/auth/password/reset", resetBody(testutil.TokenFromEmail(t, reset), newPassword)); w.Code != http.StatusNoContent {
			t.Fatalf("reset the password: status = %d; body: %s", w.Code, w.Body)
		}

		testutil.Decode[testutil.APIError](t, testutil.Do(t, api, nil, http.MethodPost, "/auth/email/confirm", confirmBody(token)), http.StatusUnprocessableEntity)
		service := auth.NewService(pool, auth.Options{})
		if _, err := service.Authenticate(t.Context(), "alice@example.com", newPassword); err != nil {
			t.Errorf("the email should not have changed: %v", err)
		}
	})
}
