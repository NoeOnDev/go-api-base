package auth

import (
	"net/http"
	"strings"
	"time"

	"github.com/NoeOnDev/go-api-base/internal/httpx"
)

// Login attempt limit per account: 10 in a row and then one per minute, from whatever IP. It
// complements the per-IP limit, which does not stop an attack spread across many IPs.
//
// It counts per email, whether it exists or not, so the response does not reveal which emails
// are registered. The cost: someone who knows an email can block that login as long as they
// keep sending attempts (one per minute is enough). A password reset resets the limit (see
// resetPassword), but that only helps once the attack is over. See docs/decisions.md.
const (
	loginLimitEvery = time.Minute
	loginLimitBurst = 10
)

// loginLimitKey is the key of an email in the login attempt limit.
func loginLimitKey(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// register answers POST /auth/register: it creates a user with role user and starts its session.
// With REQUIRE_EMAIL_VERIFICATION on, it also sends the link to confirm the email.
func (s *Service) register(w http.ResponseWriter, r *http.Request) error {
	var in RegisterInput
	if err := httpx.Decode(w, r, &in); err != nil {
		return err
	}
	// The user's language is the one the browser asked for, if it is available. If it asked for
	// none, it stays the default language (null), which follows DEFAULT_LOCALE if it changes.
	if locale, ok := s.catalog.Choose(r.Header.Get("Accept-Language")); ok {
		in.Locale = &locale
	}

	u, err := s.CreateUser(r.Context(), in, RoleUser)
	if err != nil {
		return err
	}

	if err := s.StartSession(w, r, u.ID); err != nil {
		return err
	}

	// If the email cannot be queued, the account was still created and is valid: it answers 201,
	// and the user can ask for another link with POST /auth/email/resend. Answering an error here
	// would be confusing, because retrying the registration would give 409 (the email exists).
	if s.opts.RequireVerifiedEmail {
		if err := s.sendVerificationEmail(r.Context(), u); err != nil {
			httpx.LoggerFrom(r.Context()).Error("could not send the verification email of the registration", "user_id", u.ID, "error", err)
		}
	}

	httpx.Write(w, http.StatusCreated, u)
	return nil
}

// login answers POST /auth/login. If the user has two-factor authentication on, it does not
// create the session yet: it answers 202 {"two_factor_required": true} with the challenge
// cookie, and the login is completed with POST /auth/two-factor/challenge.
func (s *Service) login(w http.ResponseWriter, r *http.Request) error {
	var in loginInput
	if err := httpx.Decode(w, r, &in); err != nil {
		return err
	}
	if err := in.validate(); err != nil {
		return err
	}
	allowed, err := s.loginLimiter.Allow(r.Context(), loginLimitKey(in.Email))
	if err != nil {
		return err
	}
	if !allowed {
		httpx.LoggerFrom(r.Context()).Warn("account login attempt limit exceeded")
		w.Header().Set("Retry-After", s.loginLimiter.RetryAfter())
		return httpx.TooManyRequests()
	}

	u, err := s.Authenticate(r.Context(), in.Email, in.Password)
	if err != nil {
		return err
	}

	if u.TwoFactorEnabled {
		return s.startTwoFactorChallenge(w, r, u.ID)
	}

	if err := s.StartSession(w, r, u.ID); err != nil {
		return err
	}

	httpx.Write(w, http.StatusOK, u)
	return nil
}

// logout answers POST /auth/logout. It deletes the session if it exists and always asks the
// browser to delete the cookie, so it answers 204 even if the session already expired.
func (s *Service) logout(w http.ResponseWriter, r *http.Request) error {
	if err := s.EndSession(r.Context(), w, r); err != nil {
		return err
	}
	httpx.Write(w, http.StatusNoContent, nil)
	return nil
}

// me answers GET /auth/me with the user of the session.
func (s *Service) me(w http.ResponseWriter, r *http.Request) error {
	httpx.Write(w, http.StatusOK, MustUser(r.Context()))
	return nil
}
