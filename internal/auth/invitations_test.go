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

func inviteBody(name, email, role string) string {
	return fmt.Sprintf(`{"name":%q,"email":%q,"role":%q}`, name, email, role)
}

func acceptBody(token, password string) string {
	return fmt.Sprintf(`{"token":%q,"password":%q}`, token, password)
}

// invite invites a user and returns them with the token of the email.
func invite(t *testing.T, api http.Handler, mailer *mail.Recorder, admin *http.Cookie, name, email, role string) (auth.AdminUser, string) {
	t.Helper()
	w := testutil.Do(t, api, admin, http.MethodPost, "/admin/users", inviteBody(name, email, role))
	u := testutil.Decode[auth.AdminUser](t, w, http.StatusCreated)
	msg, ok := mailer.Last(email)
	if !ok {
		t.Fatalf("no invitation was sent to %s", email)
	}
	return u, testutil.TokenFromEmail(t, msg)
}

func TestInviteAndAccept(t *testing.T) {
	pool := testutil.NewDB(t)
	mailer := &mail.Recorder{}
	api := testutil.NewAPI(pool, testutil.WithMailer(mailer))
	_, admin := testutil.Login(t, pool, "admin@example.com", auth.RoleAdmin)

	w := testutil.Do(t, api, admin, http.MethodPost, "/admin/users", inviteBody(" Bob ", "Bob@Example.com", auth.RoleUser))
	bob := testutil.Decode[auth.AdminUser](t, w, http.StatusCreated)
	if bob.Email != "bob@example.com" || bob.Name != "Bob" || bob.Status != auth.StatusInvited || bob.EmailVerified() {
		t.Errorf("invited user: %+v", bob)
	}

	msg, ok := mailer.Last("bob@example.com")
	if !ok || msg.Subject != "You're invited to Test App" || !strings.Contains(msg.Body, "Test User invited you") ||
		!strings.Contains(msg.Body, "expires in 7 days") || !strings.Contains(msg.HTML, "Accept the invitation") {
		t.Fatalf("invitation email: %+v", msg)
	}
	token := testutil.TokenFromEmail(t, msg)

	// Until they accept, nobody can log in to the account.
	w = testutil.Do(t, api, nil, http.MethodPost, "/auth/login", loginBodyFor("bob@example.com", "a-long-password"))
	testutil.Decode[testutil.APIError](t, w, http.StatusUnauthorized)

	// The password follows the usual rules, and a rejected one does not use up the link.
	w = testutil.Do(t, api, nil, http.MethodPost, "/auth/invitation/accept", acceptBody(token, "password1"))
	if resp := testutil.Decode[testutil.APIError](t, w, http.StatusUnprocessableEntity); resp.Error.Fields["password"] == "" {
		t.Errorf("want an error on password: %+v", resp.Error)
	}

	w = testutil.DoWithLanguage(t, api, "es-MX", nil, http.MethodPost, "/auth/invitation/accept", acceptBody(token, "a-long-password"))
	u := testutil.Decode[auth.User](t, w, http.StatusOK)
	if !u.EmailVerified() || u.Locale == nil || *u.Locale != "es-MX" {
		t.Errorf("accepted user: %+v", u)
	}
	// The session started: the cookie works.
	testutil.Decode[auth.User](t, testutil.Do(t, api, sessionCookie(t, w), http.MethodGet, "/auth/me", ""), http.StatusOK)

	// The link works only once, and the account is active.
	w = testutil.Do(t, api, nil, http.MethodPost, "/auth/invitation/accept", acceptBody(token, "another-long-password"))
	if resp := testutil.Decode[testutil.APIError](t, w, http.StatusUnprocessableEntity); resp.Error.Fields["token"] == "" {
		t.Errorf("want an error on token: %+v", resp.Error)
	}
	got := testutil.Decode[auth.AdminUser](t, testutil.Do(t, api, admin, http.MethodGet, userPath(bob.ID, ""), ""), http.StatusOK)
	if got.Status != auth.StatusActive {
		t.Errorf("status = %q, want active", got.Status)
	}
	testutil.Decode[auth.User](t, testutil.Do(t, api, nil, http.MethodPost, "/auth/login", loginBodyFor("bob@example.com", "a-long-password")), http.StatusOK)

	// There is no invitation to send again.
	w = testutil.Do(t, api, admin, http.MethodPost, userPath(bob.ID, "/invitation"), "")
	if resp := testutil.Decode[testutil.APIError](t, w, http.StatusConflict); resp.Error.Code != "invitation_accepted" {
		t.Errorf("code = %q, want invitation_accepted", resp.Error.Code)
	}
}

func TestInviteValidation(t *testing.T) {
	pool := testutil.NewDB(t)
	api := testutil.NewAPI(pool)
	_, admin := testutil.Login(t, pool, "admin@example.com", auth.RoleAdmin)

	for name, tt := range map[string]struct{ body, field string }{
		"without a name":   {inviteBody(" ", "bob@example.com", auth.RoleUser), "name"},
		"invalid email":    {inviteBody("Bob", "not-an-email", auth.RoleUser), "email"},
		"unknown role":     {inviteBody("Bob", "bob@example.com", "owner"), "role"},
		"unknown language": {`{"name":"Bob","email":"bob@example.com","role":"user","locale":"fr"}`, "locale"},
	} {
		w := testutil.Do(t, api, admin, http.MethodPost, "/admin/users", tt.body)
		if resp := testutil.Decode[testutil.APIError](t, w, http.StatusUnprocessableEntity); resp.Error.Fields[tt.field] == "" {
			t.Errorf("%s: want an error on %s: %+v", name, tt.field, resp.Error)
		}
	}

	w := testutil.Do(t, api, admin, http.MethodPost, "/admin/users", inviteBody("Admin", "ADMIN@example.com", auth.RoleUser))
	if resp := testutil.Decode[testutil.APIError](t, w, http.StatusConflict); resp.Error.Code != "email_taken" {
		t.Errorf("code = %q, want email_taken", resp.Error.Code)
	}

	testutil.Decode[testutil.APIError](t, testutil.Do(t, api, nil, http.MethodPost, "/admin/users", inviteBody("Bob", "bob@example.com", auth.RoleUser)), http.StatusUnauthorized)
	_, alice := testutil.Login(t, pool, "alice@example.com", auth.RoleUser)
	testutil.Decode[testutil.APIError](t, testutil.Do(t, api, alice, http.MethodPost, "/admin/users", inviteBody("Bob", "bob@example.com", auth.RoleUser)), http.StatusForbidden)
}

// Inviting another admin asks for the admin's password; a user, no.
func TestInviteAdminAsksForPassword(t *testing.T) {
	pool := testutil.NewDB(t)
	api := testutil.NewAPI(pool)
	_, admin := testutil.Login(t, pool, "admin@example.com", auth.RoleAdmin)

	body := func(password string) string {
		return fmt.Sprintf(`{"name":"Carol","email":"carol@example.com","role":"admin","password":%q}`, password)
	}
	for _, password := range []string{"", "wrong-password"} {
		w := testutil.Do(t, api, admin, http.MethodPost, "/admin/users", body(password))
		if resp := testutil.Decode[testutil.APIError](t, w, http.StatusUnprocessableEntity); resp.Error.Fields["password"] == "" {
			t.Errorf("password %q: want an error on password: %+v", password, resp.Error)
		}
	}
	w := testutil.Do(t, api, admin, http.MethodPost, "/admin/users", body(testutil.Password))
	if u := testutil.Decode[auth.AdminUser](t, w, http.StatusCreated); u.Role != auth.RoleAdmin || u.Status != auth.StatusInvited {
		t.Errorf("invited admin: %+v", u)
	}
}

func TestResendInvitation(t *testing.T) {
	pool := testutil.NewDB(t)
	mailer := &mail.Recorder{}
	api := testutil.NewAPI(pool, testutil.WithMailer(mailer))
	_, admin := testutil.Login(t, pool, "admin@example.com", auth.RoleAdmin)
	bob, oldToken := invite(t, api, mailer, admin, "Bob", "bob@example.com", auth.RoleUser)

	if w := testutil.Do(t, api, admin, http.MethodPost, userPath(bob.ID, "/invitation"), ""); w.Code != http.StatusNoContent {
		t.Fatalf("resend: status = %d; body: %s", w.Code, w.Body)
	}
	msg, _ := mailer.Last("bob@example.com")
	newToken := testutil.TokenFromEmail(t, msg)

	// Only the last link works.
	testutil.Decode[testutil.APIError](t,
		testutil.Do(t, api, nil, http.MethodPost, "/auth/invitation/accept", acceptBody(oldToken, "a-long-password")), http.StatusUnprocessableEntity)

	// 3 emails in a row per recipient, then 429.
	for range 2 {
		if w := testutil.Do(t, api, admin, http.MethodPost, userPath(bob.ID, "/invitation"), ""); w.Code != http.StatusNoContent {
			t.Fatalf("resend: status = %d", w.Code)
		}
	}
	w := testutil.Do(t, api, admin, http.MethodPost, userPath(bob.ID, "/invitation"), "")
	if resp := testutil.Decode[testutil.APIError](t, w, http.StatusTooManyRequests); resp.Error.Code != "too_many_emails" {
		t.Errorf("code = %q, want too_many_emails", resp.Error.Code)
	}
	testutil.Decode[testutil.APIError](t, testutil.Do(t, api, admin, http.MethodPost, userPath(999999, "/invitation"), ""), http.StatusNotFound)

	msg, _ = mailer.Last("bob@example.com")
	if token := testutil.TokenFromEmail(t, msg); token == newToken {
		t.Error("each resend should issue a new link")
	}
}

// "Forgot my password" also lets an invited user in; after that, the invitation link no longer
// works, so it cannot overwrite that password.
func TestInvitationAfterPasswordReset(t *testing.T) {
	pool := testutil.NewDB(t)
	mailer := &mail.Recorder{}
	api := testutil.NewAPI(pool, testutil.WithMailer(mailer))
	_, admin := testutil.Login(t, pool, "admin@example.com", auth.RoleAdmin)
	_, invitation := invite(t, api, mailer, admin, "Bob", "bob@example.com", auth.RoleUser)

	testutil.Do(t, api, nil, http.MethodPost, "/auth/password/forgot", `{"email":"bob@example.com"}`)
	msg, _ := mailer.Last("bob@example.com")
	reset := fmt.Sprintf(`{"token":%q,"password":"a-long-password"}`, testutil.TokenFromEmail(t, msg))
	if w := testutil.Do(t, api, nil, http.MethodPost, "/auth/password/reset", reset); w.Code != http.StatusNoContent {
		t.Fatalf("reset: status = %d; body: %s", w.Code, w.Body)
	}

	w := testutil.Do(t, api, nil, http.MethodPost, "/auth/invitation/accept", acceptBody(invitation, "another-long-password"))
	testutil.Decode[testutil.APIError](t, w, http.StatusUnprocessableEntity)
	testutil.Decode[auth.User](t, testutil.Do(t, api, nil, http.MethodPost, "/auth/login", loginBodyFor("bob@example.com", "a-long-password")), http.StatusOK)
}

// Suspending an invited user deletes their link; an invited admin does not count as an admin.
func TestInvitedUsersAndSuspension(t *testing.T) {
	pool := testutil.NewDB(t)
	mailer := &mail.Recorder{}
	api := testutil.NewAPI(pool, testutil.WithMailer(mailer))
	_, admin := testutil.Login(t, pool, "admin@example.com", auth.RoleAdmin)
	bob, token := invite(t, api, mailer, admin, "Bob", "bob@example.com", auth.RoleUser)

	list := testutil.Decode[adminUserList](t, testutil.Do(t, api, admin, http.MethodGet, "/admin/users?status=invited", ""), http.StatusOK)
	if list.Meta.Total != 1 || list.Data[0].ID != bob.ID {
		t.Errorf("invited users: %+v", list)
	}

	testutil.Decode[auth.AdminUser](t, testutil.Do(t, api, admin, http.MethodPost, userPath(bob.ID, "/suspend"), ""), http.StatusOK)
	w := testutil.Do(t, api, nil, http.MethodPost, "/auth/invitation/accept", acceptBody(token, "a-long-password"))
	testutil.Decode[testutil.APIError](t, w, http.StatusUnprocessableEntity)

	// With an invited admin (who never accepted), the only active admin is still the last one.
	w = testutil.Do(t, api, admin, http.MethodPost, "/admin/users",
		fmt.Sprintf(`{"name":"Carol","email":"carol@example.com","role":"admin","password":%q}`, testutil.Password))
	testutil.Decode[auth.AdminUser](t, w, http.StatusCreated)
	w = testutil.Do(t, api, admin, http.MethodDelete, "/auth/me", passwordBody(testutil.Password))
	if resp := testutil.Decode[testutil.APIError](t, w, http.StatusConflict); resp.Error.Code != "last_admin" {
		t.Errorf("code = %q, want last_admin", resp.Error.Code)
	}
}
