package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/NoeOnDev/go-api-base/internal/httpx"
	"github.com/NoeOnDev/go-api-base/internal/i18n"
	"github.com/NoeOnDev/go-api-base/internal/store"
)

type forgotPasswordInput struct {
	Email string `json:"email"`
}

// forgotMessage is the answer to POST /auth/password/forgot, whether the email exists or not.
func forgotMessage(r *http.Request) map[string]string {
	return map[string]string{"message": i18n.FromContext(r.Context()).T("messages.password_reset_sent")}
}

// forgotPassword answers POST /auth/password/forgot. If the email exists, it sends a single-use
// link to choose a new password. It always answers the same (202), whether the email exists or
// not, so it does not reveal which emails are registered; the email is sent in the background.
//
// If several links were already sent to the user (see allowEmail), it neither sends another one
// nor invalidates the previous one, but answers the same: that way nobody can flood their inbox
// or cancel their link.
func (s *Service) forgotPassword(w http.ResponseWriter, r *http.Request) error {
	var in forgotPasswordInput
	if err := httpx.Decode(w, r, &in); err != nil {
		return err
	}
	email := strings.ToLower(strings.TrimSpace(in.Email))
	if msg := validateEmail(email); !msg.IsZero() {
		return httpx.Validation(httpx.Fields{"email": msg})
	}

	u, err := s.q.GetUserByEmail(r.Context(), email)
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Write(w, http.StatusAccepted, forgotMessage(r))
		return nil
	}
	if err != nil {
		return fmt.Errorf("find user: %w", err)
	}
	if u.SuspendedAt != nil {
		// The link would not let them in: the login of a suspended account is rejected.
		httpx.Write(w, http.StatusAccepted, forgotMessage(r))
		return nil
	}
	allowed, err := s.allowEmail(r.Context(), purposePasswordReset, u.ID)
	if err != nil {
		return err
	}
	if !allowed {
		httpx.LoggerFrom(r.Context()).Warn("reset not sent: the user's email limit", "user_id", u.ID)
		httpx.Write(w, http.StatusAccepted, forgotMessage(r))
		return nil
	}

	token, err := s.issueToken(r.Context(), u.ID, purposePasswordReset, passwordResetTTL, nil)
	if err != nil {
		return err
	}

	if err := s.sendEmail(r.Context(), u.Email, u.Locale, "password_reset", emailData{
		Name:    u.Name,
		Link:    s.frontendLink("/reset-password", token),
		Expires: s.expiresIn(u.Locale, passwordResetTTL),
	}); err != nil {
		return err
	}

	httpx.Write(w, http.StatusAccepted, forgotMessage(r))
	return nil
}

type resetPasswordInput struct {
	Token    string `json:"token"`
	Password string `json:"password"`
}

// resetPassword answers POST /auth/password/reset with the token of the link and the new
// password. It closes every session of the user (in case someone else had signed in) and marks
// the email as verified, because the user proved they receive emails at that address.
//
// It also resets their login and two-factor attempt limits: if someone had used them up, the
// owner does not have to wait to sign in with the new password.
func (s *Service) resetPassword(w http.ResponseWriter, r *http.Request) error {
	var in resetPasswordInput
	if err := httpx.Decode(w, r, &in); err != nil {
		return err
	}

	// The password is validated first: if it breaks the rules, the token still works.
	if msg := validatePassword(in.Password); !msg.IsZero() {
		return httpx.Validation(httpx.Fields{"password": msg})
	}

	// Before computing the hash (argon2, expensive) it checks that the link works, without using it
	// up. The hash is computed outside the transaction, so a database connection is not held while
	// it waits for its turn.
	if err := s.checkToken(r.Context(), in.Token, purposePasswordReset); err != nil {
		return err
	}
	hash, err := HashPassword(r.Context(), in.Password)
	if err != nil {
		return fmt.Errorf("hash the password: %w", err)
	}

	// All or nothing: if a step fails, the link still works and nothing changed.
	var userID int64
	err = pgx.BeginFunc(r.Context(), s.db, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		id, err := consumeToken(r.Context(), q, in.Token, purposePasswordReset)
		if err != nil {
			return err
		}
		if err := savePassword(r.Context(), q, id, hash); err != nil {
			return err
		}
		if err := q.MarkEmailVerified(r.Context(), id); err != nil {
			return fmt.Errorf("mark the email verified: %w", err)
		}
		if err := q.DeleteUserSessions(r.Context(), id); err != nil {
			return fmt.Errorf("close the sessions: %w", err)
		}
		userID = id
		return nil
	})
	if err != nil {
		return err
	}
	s.sessionsEnded(r.Context(), userID, "")
	u, err := s.q.GetUserByID(r.Context(), userID)
	if err != nil {
		return fmt.Errorf("find user %d: %w", userID, err)
	}
	s.resetLimit(r.Context(), s.loginLimiter, loginLimitKey(u.Email))
	s.resetLimit(r.Context(), s.twoFactorLimiter, twoFactorLimitKey(userID))
	s.notify(r.Context(), userID, NotifyPasswordChanged, nil)

	httpx.Write(w, http.StatusNoContent, nil)
	return nil
}

// checkToken checks that the token is valid, without using it up. It avoids expensive work (like
// computing an argon2 hash) with a made-up or expired link; then it has to be consumed with
// consumeToken, which checks it again.
func (s *Service) checkToken(ctx context.Context, token, purpose string) error {
	if token == "" {
		return errInvalidToken
	}
	// Tokens sent by email do not spend attempts (attempts stays at 0): with a maximum of 1, it only
	// requires that they are still unused.
	_, err := s.q.GetUserTokenUser(ctx, store.GetUserTokenUserParams{TokenHash: hashToken(token), Purpose: purpose, MaxAttempts: 1})
	if errors.Is(err, pgx.ErrNoRows) {
		return errInvalidToken
	}
	if err != nil {
		return fmt.Errorf("find token: %w", err)
	}
	return nil
}
