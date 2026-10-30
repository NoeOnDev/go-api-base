package auth

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/NoeOnDev/go-api-base/internal/db"
	"github.com/NoeOnDev/go-api-base/internal/httpx"
	"github.com/NoeOnDev/go-api-base/internal/i18n"
	"github.com/NoeOnDev/go-api-base/internal/store"
)

type emailChangeInput struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

var errEmailTaken = httpx.Conflict("email_taken", "email")

// requestEmailChange answers PUT /auth/email with {email, password}: it asks to change the
// email. The email does not change yet: a link is sent to the new address, and it changes only
// when confirmed (POST /auth/email/confirm). That way the user proves the address is theirs.
//
// It asks for the password: otherwise someone with a stolen session would change the email,
// then reset the password with it and keep the account. The current email is also notified,
// in case the request was not made by the owner.
func (s *Service) requestEmailChange(w http.ResponseWriter, r *http.Request) error {
	var in emailChangeInput
	if err := httpx.Decode(w, r, &in); err != nil {
		return err
	}
	newEmail := strings.ToLower(strings.TrimSpace(in.Email))
	if msg := validateEmail(newEmail); !msg.IsZero() {
		return httpx.Validation(httpx.Fields{"email": msg})
	}

	user := MustUser(r.Context())
	if err := s.checkPassword(r.Context(), user.ID, in.Password, "password"); err != nil {
		return err
	}
	if newEmail == user.Email {
		return httpx.Validation(httpx.Fields{"email": i18n.M("validation.same_email")})
	}
	// Like registration, it says whether the email is in use: otherwise the user would not know
	// why it is never confirmed.
	if _, err := s.q.GetUserByEmail(r.Context(), newEmail); err == nil {
		return errEmailTaken
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("find email: %w", err)
	}

	allowed, err := s.allowEmail(r.Context(), purposeEmailChange, user.ID)
	if err != nil {
		return err
	}
	if !allowed {
		w.Header().Set("Retry-After", s.emailLimiter.RetryAfter())
		return errTooManyEmails
	}

	token, err := s.issueToken(r.Context(), user.ID, purposeEmailChange, emailChangeTTL, &newEmail)
	if err != nil {
		return err
	}
	if err := s.sendEmail(r.Context(), newEmail, user.Locale, "email_change.txt", emailData{
		Name:    user.Name,
		Link:    s.frontendLink("/confirm-email", token),
		Expires: s.expiresIn(user.Locale, emailChangeTTL),
	}); err != nil {
		return err
	}
	if err := s.sendEmail(r.Context(), user.Email, user.Locale, "email_change_notice.txt", emailData{
		Name:  user.Name,
		Email: newEmail,
	}); err != nil {
		return err
	}

	httpx.Write(w, http.StatusAccepted, map[string]string{
		"message": i18n.FromContext(r.Context()).T("messages.email_change_sent", "email", newEmail),
	})
	return nil
}

type confirmEmailInput struct {
	Token string `json:"token"`
}

// confirmEmailChange answers POST /auth/email/confirm with the token of the link. It is public,
// like the verification: the token identifies the user, so it works even if the link is opened
// on another device. It changes the email and leaves it verified.
func (s *Service) confirmEmailChange(w http.ResponseWriter, r *http.Request) error {
	var in confirmEmailInput
	if err := httpx.Decode(w, r, &in); err != nil {
		return err
	}
	if in.Token == "" {
		return errInvalidToken
	}

	// All or nothing: if another account took the new email in the meantime, the link stays unused.
	var userID int64
	var oldEmail, newEmail string
	err := pgx.BeginFunc(r.Context(), s.db, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		row, err := q.ConsumeEmailChangeToken(r.Context(), hashToken(in.Token))
		if errors.Is(err, pgx.ErrNoRows) {
			return errInvalidToken
		}
		if err != nil {
			return fmt.Errorf("consume the token: %w", err)
		}
		before, err := q.GetUserByID(r.Context(), row.UserID)
		if err != nil {
			return fmt.Errorf("find user %d: %w", row.UserID, err)
		}
		if _, err := q.UpdateUserEmail(r.Context(), store.UpdateUserEmailParams{ID: row.UserID, Email: row.Email}); err != nil {
			if _, ok := db.IsUniqueViolation(err); ok {
				return errEmailTaken
			}
			return fmt.Errorf("change the email of user %d: %w", row.UserID, err)
		}
		// The pending reset links were sent to the previous email, which no longer belongs to the
		// account: if the user changed it because they lost that inbox, whoever has it cannot use them.
		if err := q.DeleteUserTokens(r.Context(), store.DeleteUserTokensParams{UserID: row.UserID, Purpose: purposePasswordReset}); err != nil {
			return fmt.Errorf("delete the pending reset links: %w", err)
		}
		userID, oldEmail, newEmail = row.UserID, before.Email, row.Email
		return nil
	})
	if err != nil {
		return err
	}

	// The login rate limit of the old email no longer belongs to anyone.
	s.resetLimit(r.Context(), s.loginLimiter, loginLimitKey(oldEmail))
	s.notify(r.Context(), userID, NotifyEmailChanged, map[string]any{"email": newEmail})

	httpx.Write(w, http.StatusNoContent, nil)
	return nil
}
