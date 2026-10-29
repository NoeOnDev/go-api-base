package notifications_test

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/NoeOnDev/go-api-base/internal/auth"
	"github.com/NoeOnDev/go-api-base/internal/mail"
	"github.com/NoeOnDev/go-api-base/internal/testutil"
)

// The stolen session scenario: the attacker leaves a stream open, and the owner resets their
// password. The attacker's stream is cut right away, before the security notice.
func TestStolenSessionStreamIsCutOnPasswordReset(t *testing.T) {
	pool := testutil.NewDB(t)
	mails := &mail.Recorder{}
	srv := testutil.NewServer(t, pool, testutil.WithMailer(mails))
	_, owner := testutil.Login(t, pool, "alice@example.com", auth.RoleUser)

	// Another session of Alice's, started by whoever stole her password.
	resp := do(t, srv, owner, http.MethodPost, "/auth/login", `{"email":"alice@example.com","password":"test-password"}`)
	var stolen *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == auth.CookieName {
			stolen = c
		}
	}
	if stolen == nil {
		t.Fatal("the second session was not created")
	}
	attacker := testutil.OpenEvents(t, srv, stolen, "")

	// The owner resets the password.
	do(t, srv, owner, http.MethodPost, "/auth/password/forgot", `{"email":"alice@example.com"}`)
	msg, _ := mails.Last("alice@example.com")
	reset := fmt.Sprintf(`{"token":%q,"password":"a-new-password"}`, testutil.TokenFromEmail(t, msg))
	if resp := do(t, srv, owner, http.MethodPost, "/auth/password/reset", reset); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("reset: status = %d", resp.StatusCode)
	}

	// The attacker gets nothing (not even the "your password was changed" notice) and their stream
	// closes.
	attacker.Nothing(t, 200*time.Millisecond)
	if !attacker.Closed(time.Second) {
		t.Fatal("the stream of the stolen session should be cut by the password reset")
	}
}

func TestLogoutCutsItsStream(t *testing.T) {
	pool := testutil.NewDB(t)
	srv := testutil.NewServer(t, pool)
	_, cookie := testutil.Login(t, pool, "alice@example.com", auth.RoleUser)
	events := testutil.OpenEvents(t, srv, cookie, "")

	if resp := do(t, srv, cookie, http.MethodPost, "/auth/logout", ""); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("logout: status = %d", resp.StatusCode)
	}
	if !events.Closed(time.Second) {
		t.Error("the stream should be cut by the logout, without waiting for the next heartbeat")
	}
}
