package auth

import (
	"context"
	"fmt"

	"github.com/NoeOnDev/go-api-base/internal/httpx"
	"github.com/NoeOnDev/go-api-base/internal/i18n"
	"github.com/NoeOnDev/go-api-base/internal/store"
)

// Helpers shared by several account features (profile, sessions, 2FA, email change, account
// deletion). They live here and not in the file of one feature so that removing one feature (see
// "Quitar funciones" in docs/auth.md) does not break the others.

// passwordInput is the body of the actions that only ask for the password to confirm.
type passwordInput struct {
	Password string `json:"password"`
}

// checkPassword confirms the user's current password before a sensitive action.
// If it does not match, it returns a validation error on the given field.
func (s *Service) checkPassword(ctx context.Context, userID int64, password, field string) error {
	if password == "" {
		return httpx.Validation(httpx.Fields{field: i18n.M("validation.required")})
	}
	stored, err := s.q.GetUserByID(ctx, userID)
	if err != nil {
		return fmt.Errorf("find user %d: %w", userID, err)
	}
	ok, err := VerifyPassword(ctx, password, stored.PasswordHash)
	if err != nil {
		return fmt.Errorf("verify the password of user %d: %w", userID, err)
	}
	if !ok {
		return httpx.Validation(httpx.Fields{field: i18n.M("validation.wrong_password")})
	}
	return nil
}

// savePassword stores the hash of a new password with the queries q (they can be those of a
// transaction). It also deletes what was obtained with the previous password, which someone
// else could have known: the pending two-factor challenges and the pending email change (with
// it, someone would keep the email and, from there, the account).
func savePassword(ctx context.Context, q *store.Queries, userID int64, hash string) error {
	if err := q.UpdateUserPassword(ctx, store.UpdateUserPasswordParams{ID: userID, PasswordHash: hash}); err != nil {
		return fmt.Errorf("save the password of user %d: %w", userID, err)
	}
	if err := q.DeleteUserTokens(ctx, store.DeleteUserTokensParams{UserID: userID, Purpose: purposeTwoFactorLogin}); err != nil {
		return fmt.Errorf("delete the pending two-factor challenges: %w", err)
	}
	if err := q.DeleteUserTokens(ctx, store.DeleteUserTokensParams{UserID: userID, Purpose: purposeEmailChange}); err != nil {
		return fmt.Errorf("delete the pending email change: %w", err)
	}
	return nil
}
