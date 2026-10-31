package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"github.com/NoeOnDev/go-api-base/internal/httpx"
	"github.com/NoeOnDev/go-api-base/internal/store"
)

// CookieName is the name of the cookie that carries the session token.
// With CookieSecure it gets the __Host- prefix (see cookieName).
const CookieName = "session"

// hostPrefix makes the browser accept the cookie only if it is Secure, has Path=/ and has no
// Domain. That way no subdomain (for example, a compromised one) can create or overwrite it.
const hostPrefix = "__Host-"

// cookieName returns the real name of a cookie: with the __Host- prefix when cookies are Secure
// (production). It cannot be used in development, because it requires HTTPS.
func (s *Service) cookieName(base string) string {
	if s.opts.CookieSecure {
		return hostPrefix + base
	}
	return base
}

// errNoSession means the request has no valid session.
var errNoSession = errors.New("no valid session")

// newToken generates a random 32-byte (256-bit) token for the client and the hash that is stored
// in the database. Sessions and the tokens sent by email use it.
func newToken() (token string, hash []byte, err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", nil, fmt.Errorf("generate token: %w", err)
	}
	token = base64.RawURLEncoding.EncodeToString(b)
	return token, hashToken(token), nil
}

// hashToken returns the SHA-256 of the token. A fast hash (not argon2) is enough because the
// token is already random and long: it cannot be guessed by brute force.
func hashToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

// maxUserAgentLen is how much of the user-agent is stored with the session.
const maxUserAgentLen = 512

// touchInterval is how often the last activity of a session is updated. Updating it on every
// request would be a database write per request; 5 minutes is enough to show "active a while
// ago" and for the idle expiry.
const touchInterval = 5 * time.Minute

// StartSession creates a session for the user and sends the cookie. It stores the browser
// (user-agent) and the IP of the request, so the user recognizes their devices in
// GET /auth/sessions. The periodic cleanup deletes the expired sessions (internal/jobs).
//
// If the browser already had a session (another login without logout, or a change of account),
// it closes it: its cookie is replaced by the new one, so nobody would use it, but it would stay
// alive and show up as another device. The sessions of other browsers are left alone.
func (s *Service) StartSession(w http.ResponseWriter, r *http.Request, userID int64) error {
	// If it fails, the login goes on: the cleanup deletes the old session when it expires.
	if err := s.endRequestSession(r.Context(), r); err != nil {
		httpx.LoggerFrom(r.Context()).Warn("could not close the previous session of the browser", "error", err)
	}

	token, hash, err := newToken()
	if err != nil {
		return err
	}

	expires := time.Now().Add(s.opts.SessionTTL)
	if err := s.q.CreateSession(r.Context(), store.CreateSessionParams{
		TokenHash: hash,
		UserID:    userID,
		ExpiresAt: expires,
		UserAgent: truncate(strings.ToValidUTF8(r.UserAgent(), ""), maxUserAgentLen),
		IP:        httpx.ClientIP(r, s.opts.TrustedProxies),
	}); err != nil {
		return fmt.Errorf("create session: %w", err)
	}

	http.SetCookie(w, s.cookie(token, expires))
	return nil
}

// EndSession deletes the request's session (if any) and tells the browser to delete the cookie.
func (s *Service) EndSession(ctx context.Context, w http.ResponseWriter, r *http.Request) error {
	if err := s.endRequestSession(ctx, r); err != nil {
		return err
	}
	s.clearSessionCookie(w)
	return nil
}

// endRequestSession deletes the session of the request's cookie, if it has one, and tells the
// SessionWatcher to cut its streams.
func (s *Service) endRequestSession(ctx context.Context, r *http.Request) error {
	c, err := r.Cookie(s.cookieName(CookieName))
	if err != nil {
		return nil
	}
	hash := hashToken(c.Value)
	if err := s.q.DeleteSession(ctx, hash); err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	if s.opts.SessionWatcher != nil {
		s.opts.SessionWatcher.SessionEnded(ctx, hex.EncodeToString(hash))
	}
	return nil
}

// clearSessionCookie asks the browser to delete the session cookie.
func (s *Service) clearSessionCookie(w http.ResponseWriter) {
	expired := s.cookie("", time.Unix(0, 0))
	expired.MaxAge = -1
	http.SetCookie(w, expired)
}

// SessionKey identifies the request's session, or returns "" without a cookie. It is the hash of
// the token (the same one stored in the database), so it cannot be used to sign in. The
// GET /events streams use it to end when their session is closed.
func (s *Service) SessionKey(r *http.Request) string {
	c, err := r.Cookie(s.cookieName(CookieName))
	if err != nil {
		return ""
	}
	return hex.EncodeToString(hashToken(c.Value))
}

// CheckSession checks that the request's session is still valid: that it was not closed or did
// not expire, and that the user exists. It is for long connections, which went through
// RequireUser when they opened but stay open afterwards (see internal/realtime).
func (s *Service) CheckSession(r *http.Request) error {
	_, err := s.userFromRequest(r)
	return err
}

// session is the session of a request.
type session struct {
	id        int64
	tokenHash []byte
	user      User
	lastSeen  time.Time
}

// sessionFromRequest finds the session sent in the cookie, with its user.
// It returns errNoSession if there is no cookie, or the session does not exist or expired.
func (s *Service) sessionFromRequest(r *http.Request) (session, error) {
	c, err := r.Cookie(s.cookieName(CookieName))
	if err != nil {
		return session{}, errNoSession
	}

	hash := hashToken(c.Value)
	row, err := s.q.GetSessionUser(r.Context(), store.GetSessionUserParams{TokenHash: hash, ActiveSince: s.activeSince()})
	if errors.Is(err, pgx.ErrNoRows) {
		return session{}, errNoSession
	}
	if err != nil {
		return session{}, fmt.Errorf("find session: %w", err)
	}
	user := toUser(row.User)
	user.adminNeedsTwoFactor = s.opts.RequireAdminTwoFactor && user.Role == RoleAdmin && !user.TwoFactorEnabled
	user.location = s.location(r.Context(), user.TimeZone)
	return session{id: row.SessionID, tokenHash: hash, user: user, lastSeen: row.LastSeenAt}, nil
}

// userFromRequest finds the user of the session sent in the cookie.
func (s *Service) userFromRequest(r *http.Request) (User, error) {
	sess, err := s.sessionFromRequest(r)
	return sess.user, err
}

// activeSince is the time since when there must be activity for a session to keep working.
// Without an idle expiry, a minimum date.
func (s *Service) activeSince() time.Time {
	if s.opts.SessionIdleTimeout <= 0 {
		return time.Time{}
	}
	return time.Now().Add(-s.opts.SessionIdleTimeout)
}

// touch updates the last activity of the session, at most every touchInterval. If it fails, it
// is only logged: it is no reason to reject the request.
func (s *Service) touch(r *http.Request, sess session) {
	if time.Since(sess.lastSeen) < touchInterval {
		return
	}
	if err := s.q.TouchSession(r.Context(), sess.tokenHash); err != nil {
		httpx.LoggerFrom(r.Context()).Warn("could not update the session activity", "error", err)
	}
}

// truncate cuts s to at most n bytes, without splitting a character.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// cookie builds the session cookie:
//   - HttpOnly: JavaScript cannot read it, so an XSS attack cannot steal the session.
//   - SameSite=Lax: the browser does not send it in requests that another site triggers
//     (for example a hidden form), the first barrier against CSRF.
//   - Secure: it only travels over HTTPS (turned off in development, which uses http://localhost).
//   - Path=/ and no Domain: it holds for the whole API and only for the host that created it.
//     With Secure, the name has the __Host- prefix, which makes the browser enforce these three rules.
func (s *Service) cookie(value string, expires time.Time) *http.Cookie {
	return &http.Cookie{
		Name:     s.cookieName(CookieName),
		Value:    value,
		Path:     "/",
		Expires:  expires,
		HttpOnly: true,
		Secure:   s.opts.CookieSecure,
		SameSite: http.SameSiteLaxMode,
	}
}

// deleteOtherSessions deletes every session of the user except the one of this request, with the
// queries q (they can be those of a transaction). It returns the key of the remaining session,
// to notify with sessionsEnded after the commit.
func (s *Service) deleteOtherSessions(r *http.Request, q *store.Queries, userID int64) (string, error) {
	c, err := r.Cookie(s.cookieName(CookieName))
	if err != nil {
		return "", fmt.Errorf("read the session cookie: %w", err)
	}
	hash := hashToken(c.Value)
	if err := q.DeleteOtherSessions(r.Context(), store.DeleteOtherSessionsParams{
		UserID:    userID,
		TokenHash: hash,
	}); err != nil {
		return "", fmt.Errorf("close the other sessions: %w", err)
	}
	return hex.EncodeToString(hash), nil
}

// sessionsEnded tells the SessionWatcher that the user's sessions were closed except keep (empty:
// all of them), to cut the streams opened with them. It goes after the commit: before it, a cut
// stream could reconnect with a session that was not deleted yet.
func (s *Service) sessionsEnded(ctx context.Context, userID int64, keep string) {
	if s.opts.SessionWatcher != nil {
		s.opts.SessionWatcher.UserSessionsEnded(ctx, userID, keep)
	}
}
