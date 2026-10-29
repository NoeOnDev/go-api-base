package auth

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/NoeOnDev/go-api-base/internal/httpx"
	"github.com/NoeOnDev/go-api-base/internal/i18n"
	"github.com/NoeOnDev/go-api-base/internal/store"
)

// sessionResponse is a session as the user sees it. The token hash never leaves the API: it is
// identified by id. The user-agent goes as is; the frontend can show it in a readable way
// ("Firefox on Windows") with a library like ua-parser-js.
type sessionResponse struct {
	ID         int64     `json:"id"`
	UserAgent  string    `json:"user_agent"`
	IP         string    `json:"ip"`
	CreatedAt  time.Time `json:"created_at"`
	LastSeenAt time.Time `json:"last_seen_at"`
	// Current marks the session of this request (this device).
	Current bool `json:"current"`
}

// listSessions answers GET /auth/sessions: the open sessions of the user (their devices), from
// the most recent to the oldest. Those expired for inactivity do not show up, even if the
// periodic cleanup has not deleted them yet: they can no longer be used to sign in.
func (s *Service) listSessions(w http.ResponseWriter, r *http.Request) error {
	rows, err := s.q.ListUserSessions(r.Context(), store.ListUserSessionsParams{
		UserID:      MustUser(r.Context()).ID,
		ActiveSince: s.activeSince(),
	})
	if err != nil {
		return fmt.Errorf("list sessions: %w", err)
	}

	current := currentSessionID(r.Context())
	data := make([]sessionResponse, len(rows))
	for i, row := range rows {
		data[i] = sessionResponse{
			ID: row.ID, UserAgent: row.UserAgent, IP: row.IP,
			CreatedAt: row.CreatedAt, LastSeenAt: row.LastSeenAt,
			Current: row.ID == current,
		}
	}
	httpx.Write(w, http.StatusOK, map[string][]sessionResponse{"data": data})
	return nil
}

var errCurrentSession = httpx.Validation(httpx.Fields{"id": i18n.M("validation.current_session")})

// revokeSession answers DELETE /auth/sessions/{id}: it closes a session of the user, for example
// that of a device they do not recognize. Another user's answers 404, as in notes. What depended
// on it (its GET /events streams) is cut right away.
func (s *Service) revokeSession(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.PathID(r, "id")
	if err != nil {
		return err
	}
	if id == currentSessionID(r.Context()) {
		return errCurrentSession
	}

	c, err := r.Cookie(s.cookieName(CookieName))
	if err != nil {
		return fmt.Errorf("read the session cookie: %w", err)
	}
	hash, err := s.q.DeleteUserSession(r.Context(), store.DeleteUserSessionParams{
		ID:        id,
		UserID:    MustUser(r.Context()).ID,
		TokenHash: hashToken(c.Value),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return httpx.NotFound("session_not_found")
	}
	if err != nil {
		return fmt.Errorf("close session %d: %w", id, err)
	}
	if s.opts.SessionWatcher != nil {
		s.opts.SessionWatcher.SessionEnded(r.Context(), hex.EncodeToString(hash))
	}

	httpx.Write(w, http.StatusNoContent, nil)
	return nil
}

// revokeOtherSessions answers POST /auth/sessions/revoke-others with {password}: it closes every
// session of the user except the current one. It asks for the password, like Laravel Jetstream,
// because it is a bulk action: that way someone with a stolen session cannot kick the owner out
// of all their devices.
func (s *Service) revokeOtherSessions(w http.ResponseWriter, r *http.Request) error {
	var in passwordInput
	if err := httpx.Decode(w, r, &in); err != nil {
		return err
	}
	user := MustUser(r.Context())
	if err := s.checkPassword(r.Context(), user.ID, in.Password, "password"); err != nil {
		return err
	}
	if err := s.endOtherSessions(r, user.ID); err != nil {
		return err
	}

	httpx.Write(w, http.StatusNoContent, nil)
	return nil
}

// currentSessionID returns the ID of the request's session, on routes with a session.
func currentSessionID(ctx context.Context) int64 {
	id, _ := ctx.Value(sessionIDKey{}).(int64)
	return id
}

// endOtherSessions closes every session of the user except the one of this request, and tells
// the SessionWatcher. It is for when there is nothing else to save together
// (POST /auth/sessions/revoke-others); otherwise, use deleteOtherSessions inside the transaction
// and sessionsEnded after the commit.
func (s *Service) endOtherSessions(r *http.Request, userID int64) error {
	keep, err := s.deleteOtherSessions(r, s.q, userID)
	if err != nil {
		return err
	}
	s.sessionsEnded(r.Context(), userID, keep)
	return nil
}
