package auth

import (
	"context"
	"errors"
	"net/http"

	"github.com/NoeOnDev/go-api-base/internal/httpx"
)

type ctxKey struct{}

// sessionIDKey stores in the context the ID of the request's session (see currentSessionID).
type sessionIDKey struct{}

// RequireUser lets through only requests with a valid session (otherwise it answers 401) and
// stores the user in the context. Handlers get it with UserFrom or MustUser.
//
// With REQUIRE_EMAIL_VERIFICATION on, it also requires a verified email: if it is not, it
// answers 403 with the code "email_not_verified", so the frontend shows the notice.
func (s *Service) RequireUser(next http.Handler) http.Handler {
	if !s.opts.RequireVerifiedEmail {
		return s.requireSession(next)
	}
	return s.requireSession(httpx.Handle(func(w http.ResponseWriter, r *http.Request) error {
		if !MustUser(r.Context()).EmailVerified() {
			return errEmailNotVerified
		}
		next.ServeHTTP(w, r)
		return nil
	}))
}

var (
	errEmailNotVerified       = &httpx.Error{Status: http.StatusForbidden, Code: "email_not_verified"}
	errAdminTwoFactorRequired = &httpx.Error{Status: http.StatusForbidden, Code: "admin_two_factor_required"}
)

// requireSession requires a valid session, but not a verified email. The routes of the
// account itself use it (see the profile, sign out, resend the verification), which must work
// even if the user has not confirmed their email yet.
func (s *Service) requireSession(next http.Handler) http.Handler {
	return httpx.Handle(func(w http.ResponseWriter, r *http.Request) error {
		sess, err := s.sessionFromRequest(r)
		if errors.Is(err, errNoSession) {
			return httpx.Unauthorized("unauthorized")
		}
		if err != nil {
			return err
		}
		s.touch(r, sess)

		// From here on, every log line of the request (including the final one) includes the user.
		httpx.AddLogAttrs(r.Context(), "user_id", sess.user.ID)

		ctx := context.WithValue(WithUser(r.Context(), sess.user), sessionIDKey{}, sess.id)
		next.ServeHTTP(w, r.WithContext(ctx))
		return nil
	})
}

// OptionalUser identifies the user if the request has a valid session, but does not require it:
// without a session (or with an expired one) the request goes on as anonymous. It is for public
// routes that show more data to some users; in the handler, it is read with UserFrom.
func (s *Service) OptionalUser(next http.Handler) http.Handler {
	return httpx.Handle(func(w http.ResponseWriter, r *http.Request) error {
		sess, err := s.sessionFromRequest(r)
		if errors.Is(err, errNoSession) {
			next.ServeHTTP(w, r)
			return nil
		}
		if err != nil {
			return err
		}
		s.touch(r, sess)

		httpx.AddLogAttrs(r.Context(), "user_id", sess.user.ID)
		ctx := context.WithValue(WithUser(r.Context(), sess.user), sessionIDKey{}, sess.id)
		next.ServeHTTP(w, r.WithContext(ctx))
		return nil
	})
}

// RequireAdmin lets through only users with the admin role: 401 without a session, 403 with
// another role. With RequireAdminTwoFactor, an admin without two-factor authentication gets 403
// admin_two_factor_required: the /auth routes only ask for a session, so they can turn it on.
func (s *Service) RequireAdmin(next http.Handler) http.Handler {
	return s.RequireUser(httpx.Handle(func(w http.ResponseWriter, r *http.Request) error {
		user := MustUser(r.Context())
		if user.adminNeedsTwoFactor {
			return errAdminTwoFactorRequired
		}
		if !user.IsAdmin() {
			return httpx.Forbidden("forbidden")
		}
		next.ServeHTTP(w, r)
		return nil
	}))
}

// WithUser returns a context with the authenticated user. The middlewares use it; it also helps
// to test a protected handler without going through the login.
func WithUser(ctx context.Context, u User) context.Context {
	return context.WithValue(ctx, ctxKey{}, u)
}

// UserFrom returns the authenticated user of the request, if there is one.
func UserFrom(ctx context.Context) (User, bool) {
	u, ok := ctx.Value(ctxKey{}).(User)
	return u, ok
}

// MustUser returns the authenticated user. Use it only in handlers protected with RequireUser
// or RequireAdmin: without a user it is a programming error and it panics (the Recover
// middleware answers 500 and logs it).
func MustUser(ctx context.Context) User {
	u, ok := UserFrom(ctx)
	if !ok {
		panic("auth.MustUser: the route is not protected with RequireUser")
	}
	return u
}
