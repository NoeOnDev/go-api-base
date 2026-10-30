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

func verifyBody(token string) string { return fmt.Sprintf(`{"token":%q}`, token) }

func TestEmailVerificationRequired(t *testing.T) {
	pool := testutil.NewDB(t)
	mails := &mail.Recorder{}
	api := testutil.NewAPI(pool, testutil.WithMailer(mails), testutil.WithConfig(requireVerification))

	// On registration the link is sent, and the user stays unverified.
	w := testutil.Do(t, api, nil, http.MethodPost, "/auth/register", registerBody)
	u := testutil.Decode[auth.User](t, w, http.StatusCreated)
	if u.EmailVerified() {
		t.Fatal("the user should not be verified on registration")
	}
	cookie := sessionCookie(t, w)

	first, ok := mails.Last("alice@example.com")
	if !ok || !strings.Contains(first.Body, "https://app.test/verify-email?token=") {
		t.Fatalf("the verification link was not sent: %+v", mails.Messages())
	}

	// Unverified: the protected routes answer 403, but the account is still reachable.
	resp := testutil.Decode[testutil.APIError](t, testutil.Do(t, api, cookie, http.MethodGet, "/notes", ""), http.StatusForbidden)
	if resp.Error.Code != "email_not_verified" {
		t.Errorf("code = %q", resp.Error.Code)
	}
	for _, tt := range []struct{ method, path, body string }{
		{http.MethodGet, "/auth/me", ""},
		{http.MethodPut, "/auth/me", `{"name":"Alice"}`},
	} {
		if w := testutil.Do(t, api, cookie, tt.method, tt.path, tt.body); w.Code != http.StatusOK {
			t.Errorf("%s %s unverified: status = %d", tt.method, tt.path, w.Code)
		}
	}

	// Asking for another link invalidates the previous one.
	if w := testutil.Do(t, api, cookie, http.MethodPost, "/auth/email/resend", ""); w.Code != http.StatusAccepted {
		t.Fatalf("resend: status = %d", w.Code)
	}
	second, _ := mails.Last("alice@example.com")
	testutil.Decode[testutil.APIError](t,
		testutil.Do(t, api, nil, http.MethodPost, "/auth/email/verify", verifyBody(testutil.TokenFromEmail(t, first))),
		http.StatusUnprocessableEntity)

	// With the new link it is verified, and the protected routes work.
	if w := testutil.Do(t, api, nil, http.MethodPost, "/auth/email/verify", verifyBody(testutil.TokenFromEmail(t, second))); w.Code != http.StatusNoContent {
		t.Fatalf("verify: status = %d; body: %s", w.Code, w.Body)
	}
	me := testutil.Decode[auth.User](t, testutil.Do(t, api, cookie, http.MethodGet, "/auth/me", ""), http.StatusOK)
	if !me.EmailVerified() {
		t.Error("the email was not verified")
	}
	if w := testutil.Do(t, api, cookie, http.MethodGet, "/notes", ""); w.Code != http.StatusOK {
		t.Errorf("/notes after verifying: status = %d", w.Code)
	}

	// Once verified, asking for another link sends nothing.
	sent := len(mails.Messages())
	if w := testutil.Do(t, api, cookie, http.MethodPost, "/auth/email/resend", ""); w.Code != http.StatusNoContent {
		t.Errorf("resend when already verified: status = %d", w.Code)
	}
	if len(mails.Messages()) != sent {
		t.Error("an email was sent to an already verified address")
	}
}

func TestEmailVerificationDisabledByDefault(t *testing.T) {
	pool := testutil.NewDB(t)
	mails := &mail.Recorder{}
	api := testutil.NewAPI(pool, testutil.WithMailer(mails))

	w := testutil.Do(t, api, nil, http.MethodPost, "/auth/register", registerBody)
	testutil.Decode[auth.User](t, w, http.StatusCreated)

	if len(mails.Messages()) != 0 {
		t.Errorf("should not send emails on registration: %+v", mails.Messages())
	}
	if w := testutil.Do(t, api, sessionCookie(t, w), http.MethodGet, "/notes", ""); w.Code != http.StatusOK {
		t.Errorf("without required verification, /notes should work: status = %d", w.Code)
	}
}

func TestAdminsAreVerified(t *testing.T) {
	pool := testutil.NewDB(t)
	api := testutil.NewAPI(pool, testutil.WithConfig(requireVerification))
	admin, cookie := testutil.Login(t, pool, "admin@example.com", auth.RoleAdmin)

	if !admin.EmailVerified() {
		t.Error("an admin should be verified when created")
	}
	if w := testutil.Do(t, api, cookie, http.MethodPost, "/categories", `{"name":"Drinks","slug":"drinks"}`); w.Code != http.StatusCreated {
		t.Errorf("the admin cannot use protected routes: status = %d", w.Code)
	}
}

func TestVerifyEmailInvalidToken(t *testing.T) {
	pool := testutil.NewDB(t)
	api := testutil.NewAPI(pool)

	resp := testutil.Decode[testutil.APIError](t,
		testutil.Do(t, api, nil, http.MethodPost, "/auth/email/verify", verifyBody("made-up-token")),
		http.StatusUnprocessableEntity)
	if resp.Error.Fields["token"] == "" {
		t.Errorf("want an error on token: %+v", resp.Error)
	}
	testutil.Decode[testutil.APIError](t, testutil.Do(t, api, nil, http.MethodPost, "/auth/email/resend", ""), http.StatusUnauthorized)
}

// The per-recipient limits stop whoever requests emails for someone else from many IPs: each
// request comes from a different IP, so the per-IP limit does not step in.
func TestResendVerificationRecipientLimit(t *testing.T) {
	pool := testutil.NewDB(t)
	mails := &mail.Recorder{}
	api := testutil.NewAPI(pool, testutil.WithMailer(mails), testutil.WithConfig(requireVerification))

	w := testutil.Do(t, api, nil, http.MethodPost, "/auth/register", registerBody)
	testutil.Decode[auth.User](t, w, http.StatusCreated)
	cookie := sessionCookie(t, w)

	// The registration email does not count: after it, 3 more can be requested.
	for i := range 4 {
		ip := fmt.Sprintf("198.51.100.%d", i+1)
		w := testutil.DoFromIP(t, api, ip, cookie, http.MethodPost, "/auth/email/resend", "")
		if i < 3 {
			if w.Code != http.StatusAccepted {
				t.Fatalf("resend %d: status = %d", i+1, w.Code)
			}
			continue
		}
		resp := testutil.Decode[testutil.APIError](t, w, http.StatusTooManyRequests)
		if resp.Error.Code != "too_many_emails" || w.Header().Get("Retry-After") != "1200" {
			t.Errorf("unexpected response: %+v, Retry-After %q", resp.Error, w.Header().Get("Retry-After"))
		}
	}

	if n := len(mails.Messages()); n != 4 {
		t.Errorf("%d emails were sent, want 4 (registration and 3 resends)", n)
	}
}
