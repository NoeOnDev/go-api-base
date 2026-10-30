package auth

import (
	"net/http"
	"strconv"

	"github.com/NoeOnDev/go-api-base/internal/httpx"
	"github.com/NoeOnDev/go-api-base/internal/ratelimit"
)

// Limits are the rate limits of the auth routes, one per group, so one group does not spend the
// attempts of another (for example, the second step of the login does not spend the login's).
// The values are chosen in server/routes.go.
type Limits struct {
	// Login, Register and TwoFactor (the second step of the login) are limited per IP.
	Login, Register, TwoFactor httpx.Middleware
	// Tokens: the routes that receive a token from an email (password reset, email verification or
	// confirmation). Per IP.
	Tokens httpx.Middleware
	// Emails: the routes that send emails. Per IP; there is also a per-recipient limit.
	Emails httpx.Middleware
	// Account: the sensitive actions with a session (changing the password, two-factor…). It is
	// limited per user and not per IP: the attack would come from someone with a session, and that
	// way users behind the same IP (an office, mobile data) do not share the limit. It is in the
	// database, like the other per-user limits, so it holds for every instance.
	Account *ratelimit.Limiter
}

// Register adds the authentication routes to the mux.
//
// To remove a feature a project does not need, delete its routes here (and its file:
// password_reset.go, profile.go…). "Removing features" in docs/auth.md says what else changes.
func Register(mux *http.ServeMux, s *Service, limits Limits) {
	// No /auth response is cached: some carry secrets (the two-factor one, the recovery codes) and
	// the rest, account data. NoStore goes outside the limits so the 429s carry it too.
	handle := func(pattern string, h http.Handler) { mux.Handle(pattern, httpx.NoStore(h)) }

	// account limits per user: it goes inside RequireUser or requireSession, which set the user.
	account := limits.Account.Middleware(func(r *http.Request) string {
		return strconv.FormatInt(MustUser(r.Context()).ID, 10)
	})

	// Registration and sign in.
	handle("POST /auth/register", limits.Register(httpx.Handle(s.register)))
	handle("POST /auth/login", limits.Login(httpx.Handle(s.login)))
	// Logout does not require a valid session: if it expired, it still has to delete the browser cookie.
	handle("POST /auth/logout", httpx.Handle(s.logout))

	// The user's account.
	handle("GET /auth/me", s.requireSession(httpx.Handle(s.me)))
	handle("PUT /auth/me", s.requireSession(httpx.Handle(s.updateProfile)))
	handle("DELETE /auth/me", s.requireSession(account(httpx.Handle(s.deleteAccount))))
	handle("PUT /auth/password", s.RequireUser(account(httpx.Handle(s.changePassword))))

	// Sessions per device: list them and close the ones not recognized.
	handle("GET /auth/sessions", s.requireSession(httpx.Handle(s.listSessions)))
	handle("DELETE /auth/sessions/{id}", s.requireSession(account(httpx.Handle(s.revokeSession))))
	handle("POST /auth/sessions/revoke-others", s.requireSession(account(httpx.Handle(s.revokeOtherSessions))))

	// Password reset by email.
	handle("POST /auth/password/forgot", limits.Emails(httpx.Handle(s.forgotPassword)))
	handle("POST /auth/password/reset", limits.Tokens(httpx.Handle(s.resetPassword)))

	// Two-factor authentication with an authenticator app (optional for each user).
	handle("POST /auth/two-factor/challenge", limits.TwoFactor(httpx.Handle(s.twoFactorChallenge)))
	handle("POST /auth/two-factor/setup", s.RequireUser(httpx.Handle(s.setupTwoFactor)))
	handle("POST /auth/two-factor/enable", s.RequireUser(account(httpx.Handle(s.enableTwoFactor))))
	handle("POST /auth/two-factor/disable", s.RequireUser(account(httpx.Handle(s.disableTwoFactor))))
	handle("POST /auth/two-factor/recovery-codes", s.RequireUser(account(httpx.Handle(s.regenerateRecoveryCodes))))

	// Email verification (required only with REQUIRE_EMAIL_VERIFICATION=true).
	handle("POST /auth/email/verify", limits.Tokens(httpx.Handle(s.verifyEmail)))
	handle("POST /auth/email/resend", limits.Emails(s.requireSession(httpx.Handle(s.resendVerification))))

	// Email change: it is requested with the password and confirmed with the link sent to the new one.
	handle("PUT /auth/email", s.requireSession(account(httpx.Handle(s.requestEmailChange))))
	handle("POST /auth/email/confirm", limits.Tokens(httpx.Handle(s.confirmEmailChange)))

	// User management, for administrators. Changing the role and deleting ask for the admin's
	// password, so they have the per-user limit.
	handle("GET /admin/users", s.RequireAdmin(httpx.Handle(s.listUsers)))
	handle("GET /admin/users/{id}", s.RequireAdmin(httpx.Handle(s.getUser)))
	handle("PUT /admin/users/{id}/role", s.RequireAdmin(account(httpx.Handle(s.changeRole))))
	handle("POST /admin/users/{id}/suspend", s.RequireAdmin(httpx.Handle(s.suspendUser)))
	handle("POST /admin/users/{id}/unsuspend", s.RequireAdmin(httpx.Handle(s.unsuspendUser)))
	handle("DELETE /admin/users/{id}", s.RequireAdmin(account(httpx.Handle(s.deleteUserByAdmin))))

	// Invitations: an admin creates the account, and the person chooses their password from the
	// link. Inviting another admin asks for the admin's password, with the per-user limit.
	handle("POST /admin/users", s.RequireAdmin(httpx.Handle(s.inviteUser(limits.Account))))
	handle("POST /admin/users/{id}/invitation", s.RequireAdmin(httpx.Handle(s.resendInvitation)))
	handle("POST /auth/invitation/accept", limits.Tokens(httpx.Handle(s.acceptInvitation)))
}
