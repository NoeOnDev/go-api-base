// Package auth implements authentication: registration, login with sessions stored in
// PostgreSQL and sent in an HttpOnly cookie, roles (user/admin) and the middlewares that
// protect the routes.
package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/NoeOnDev/go-api-base/internal/db"
	"github.com/NoeOnDev/go-api-base/internal/httpx"
	"github.com/NoeOnDev/go-api-base/internal/i18n"
	"github.com/NoeOnDev/go-api-base/internal/mail"
	"github.com/NoeOnDev/go-api-base/internal/ratelimit"
	"github.com/NoeOnDev/go-api-base/internal/secretbox"
	"github.com/NoeOnDev/go-api-base/internal/store"
)

// User roles.
const (
	RoleUser  = "user"
	RoleAdmin = "admin"
)

// DefaultSessionTTL is used when the session duration is not configured.
const DefaultSessionTTL = 7 * 24 * time.Hour

// User is the authenticated user. It is what the handlers receive and what the API returns:
// it never includes the password hash.
type User struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
	Role  string `json:"role"`
	// EmailVerifiedAt is null until the user confirms their email.
	EmailVerifiedAt  *time.Time `json:"email_verified_at"`
	TwoFactorEnabled bool       `json:"two_factor_enabled"`
	// Locale is the language of the user's emails and notifications, for example "es-MX".
	// null is the default language (DEFAULT_LOCALE). See docs/i18n.md.
	Locale    *string   `json:"locale"`
	CreatedAt time.Time `json:"created_at"`

	// adminNeedsTwoFactor marks an admin without two-factor authentication when
	// RequireAdminTwoFactor is on. The session sets it (see sessionFromRequest).
	adminNeedsTwoFactor bool
}

// EmailVerified reports whether the user confirmed their email.
func (u User) EmailVerified() bool { return u.EmailVerifiedAt != nil }

// IsAdmin reports whether the user can act as an administrator: they have the admin role and,
// if RequireAdminTwoFactor (REQUIRE_ADMIN_TWO_FACTOR) is on, two-factor authentication. Check
// admin permissions with it, never by comparing Role.
func (u User) IsAdmin() bool { return u.Role == RoleAdmin && !u.adminNeedsTwoFactor }

func toUser(u store.User) User {
	return User{
		ID:               u.ID,
		Name:             u.Name,
		Email:            u.Email,
		Role:             u.Role,
		EmailVerifiedAt:  u.EmailVerifiedAt,
		TwoFactorEnabled: u.TOTPEnabledAt != nil,
		Locale:           u.Locale,
		CreatedAt:        u.CreatedAt,
	}
}

// Options configures the authentication service.
type Options struct {
	// SessionTTL is how long a session lasts. If 0, DefaultSessionTTL is used.
	SessionTTL time.Duration
	// SessionIdleTimeout closes a session without activity for that long. 0 turns it off.
	SessionIdleTimeout time.Duration
	// CookieSecure makes the cookie travel only over HTTPS. It must be true in production.
	CookieSecure bool
	// Mailer sends the emails (password reset, email verification). If nil, the emails are
	// written to the log.
	Mailer mail.Sender
	// FrontendURL is the base of the links in the emails, for example https://app.client.com.
	FrontendURL string
	// RequireVerifiedEmail requires a verified email in RequireUser and RequireAdmin.
	RequireVerifiedEmail bool
	// RequireAdminTwoFactor requires administrators to have two-factor authentication on: without
	// it, User.IsAdmin is false and RequireAdmin answers 403 admin_two_factor_required.
	RequireAdminTwoFactor bool
	// Box encrypts the two-factor secret and protects the recovery codes. Without it, two-factor authentication is not available.
	Box *secretbox.Box
	// Issuer is the application name that the authenticator app shows.
	Issuer string
	// TrustedProxies are the proxies whose X-Forwarded-For is accepted (see httpx.ClientIP).
	// They are used to record the real client IP (for example, in the email that warns about wrong
	// two-factor codes).
	TrustedProxies httpx.TrustedProxies
	// I18n are the languages of the emails and of the user-facing texts. If nil, i18n.Default()
	// is used (English by default).
	I18n *i18n.Catalog
	// SessionWatcher learns when auth closes sessions, to cut what depended on them.
	// It is optional. In the template, the internal/realtime broker implements it, cutting the
	// GET /events streams opened with those sessions.
	SessionWatcher SessionWatcher
	// Notifier receives the security notices of the account (password changed, two-factor
	// authentication enabled or disabled). It is optional: if nil, auth does not notify. In the
	// template internal/notifications implements it, so auth does not depend on that module.
	Notifier Notifier
}

// SessionWatcher receives the closed sessions. sessionKey is the one of Service.SessionKey.
type SessionWatcher interface {
	// SessionEnded reports that a session was closed (logout).
	SessionEnded(ctx context.Context, sessionKey string)
	// UserSessionsEnded reports that every session of the user was closed except keepSessionKey
	// (empty: all of them).
	UserSessionsEnded(ctx context.Context, userID int64, keepSessionKey string)
}

// Notifier sends a notification to a user (see internal/notifications).
type Notifier interface {
	Notify(ctx context.Context, userID int64, kind string, data any) error
}

// Kinds of the security notifications that auth sends.
const (
	NotifyPasswordChanged   = "password_changed"
	NotifyTwoFactorEnabled  = "two_factor_enabled"
	NotifyTwoFactorDisabled = "two_factor_disabled"
	NotifyEmailChanged      = "email_changed"
)

// notify tells the user about a security change in their account. data is the data of the
// notice (nil if it has none): the text is not stored, internal/notifications builds it in the
// language of whoever reads it (see docs/i18n.md). If it fails, it is only logged: the change
// was already made, and answering an error because of the notice makes no sense.
func (s *Service) notify(ctx context.Context, userID int64, kind string, data map[string]any) {
	if s.opts.Notifier == nil {
		return
	}
	if data == nil {
		data = map[string]any{} // stored as {} and not null
	}
	if err := s.opts.Notifier.Notify(ctx, userID, kind, data); err != nil {
		httpx.LoggerFrom(ctx).Warn("could not send the notification", "user_id", userID, "kind", kind, "error", err)
	}
}

// resetLimit gives the key back all its attempts. If it fails, it is only logged: what the limit
// protected already changed, and the key refills on its own over time.
func (s *Service) resetLimit(ctx context.Context, l *ratelimit.Limiter, key string) {
	if err := l.Reset(ctx, key); err != nil {
		httpx.LoggerFrom(ctx).Warn("could not reset a rate limit", "error", err)
	}
}

// Service groups the authentication logic and its dependencies.
type Service struct {
	db   *pgxpool.Pool // for the transactions (pgx.BeginFunc)
	q    *store.Queries
	opts Options

	// Limits that do not depend on the IP (see handlers.go, two_factor_login.go and emails.go).
	// They are in the database, so they hold for every API instance.
	loginLimiter     *ratelimit.Limiter
	twoFactorLimiter *ratelimit.Limiter
	emailLimiter     *ratelimit.Limiter

	catalog *i18n.Catalog
	emails  *mail.Templates // email templates, per language
}

// NewService creates the authentication service. It takes the pool, and not only the queries,
// because some operations run in a transaction (for example, the password reset).
func NewService(pool *pgxpool.Pool, opts Options) *Service {
	if opts.SessionTTL <= 0 {
		opts.SessionTTL = DefaultSessionTTL
	}
	if opts.Mailer == nil {
		opts.Mailer = mail.LogSender{Logger: slog.Default()}
	}
	if opts.FrontendURL == "" {
		opts.FrontendURL = "http://localhost:5173"
	}
	if opts.Issuer == "" {
		opts.Issuer = "My App"
	}
	if opts.I18n == nil {
		opts.I18n = i18n.Default()
	}
	q := store.New(pool)
	return &Service{
		db:               pool,
		q:                q,
		opts:             opts,
		catalog:          opts.I18n,
		emails:           newEmailTemplates(opts.I18n),
		loginLimiter:     ratelimit.New(q, "login", loginLimitEvery, loginLimitBurst),
		twoFactorLimiter: ratelimit.New(q, "two_factor", twoFactorLimitEvery, twoFactorLimitBurst),
		emailLimiter:     ratelimit.New(q, "email", emailLimitEvery, emailLimitBurst),
	}
}

// CreateUser validates the data, hashes the password and creates the user with the given role.
// Registration (role user) and the create-admin command (role admin) use it. Admins get a
// verified email: they are created by someone with access to the server.
// Validation errors and a repeated email are returned as *httpx.Error.
func (s *Service) CreateUser(ctx context.Context, in RegisterInput, role string) (User, error) {
	in.normalize()
	if err := in.validate(); err != nil {
		return User{}, err
	}
	if role != RoleUser && role != RoleAdmin {
		return User{}, fmt.Errorf("rol desconocido %q", role)
	}

	hash, err := HashPassword(ctx, in.Password)
	if err != nil {
		return User{}, fmt.Errorf("hash the password: %w", err)
	}

	var verifiedAt *time.Time
	if role == RoleAdmin {
		now := time.Now()
		verifiedAt = &now
	}

	u, err := s.q.CreateUser(ctx, store.CreateUserParams{
		Name:            in.Name,
		Email:           in.Email,
		PasswordHash:    hash,
		Role:            role,
		EmailVerifiedAt: verifiedAt,
		Locale:          in.Locale,
	})
	if _, ok := db.IsUniqueViolation(err); ok {
		return User{}, httpx.Conflict("email_taken", "email")
	}
	if err != nil {
		return User{}, fmt.Errorf("create user: %w", err)
	}

	return toUser(u), nil
}

// errInvalidCredentials is the answer to any failed login. It does not tell "the email does
// not exist" from "the password is wrong", so it does not reveal which emails are registered.
var errInvalidCredentials = httpx.Unauthorized("invalid_credentials")

// Authenticate checks the email and password and returns the user.
func (s *Service) Authenticate(ctx context.Context, email, password string) (User, error) {
	u, err := s.q.GetUserByEmail(ctx, strings.ToLower(strings.TrimSpace(email)))
	if errors.Is(err, pgx.ErrNoRows) {
		// A hash is computed anyway, so the response takes as long as with a valid email.
		_, _ = VerifyPassword(ctx, password, dummyHash())
		return User{}, errInvalidCredentials
	}
	if err != nil {
		return User{}, fmt.Errorf("find user: %w", err)
	}

	ok, err := VerifyPassword(ctx, password, u.PasswordHash)
	if err != nil {
		return User{}, fmt.Errorf("verify the password of user %d: %w", u.ID, err)
	}
	if !ok {
		return User{}, errInvalidCredentials
	}
	if needsRehash(u.PasswordHash) {
		s.rehashPassword(ctx, u, password)
	}

	return toUser(u), nil
}

// rehashPassword hashes the password again with the current argon2 parameters, after a
// successful login (the only moment the password is known). If it fails, it is only logged: the
// login was already valid, and it is tried again on the next one.
func (s *Service) rehashPassword(ctx context.Context, u store.User, password string) {
	hash, err := HashPassword(ctx, password)
	if err == nil {
		_, err = s.q.RehashPassword(ctx, store.RehashPasswordParams{ID: u.ID, OldHash: u.PasswordHash, NewHash: hash})
	}
	if err != nil {
		httpx.LoggerFrom(ctx).Warn("could not update the password hash", "user_id", u.ID, "error", err)
	}
}
