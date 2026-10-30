package auth

import (
	"context"
	"fmt"
	"slices"

	"github.com/jackc/pgx/v5"

	"github.com/NoeOnDev/go-api-base/internal/db"
	"github.com/NoeOnDev/go-api-base/internal/httpx"
	"github.com/NoeOnDev/go-api-base/internal/i18n"
	"github.com/NoeOnDev/go-api-base/internal/store"
)

// Helpers shared by several account features (profile, sessions, 2FA, email change, account
// deletion, user management). They live here and not in the file of one feature so that removing
// one feature (see "Removing features" in docs/auth.md) does not break the others.

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

var (
	errLastAdmin    = httpx.Conflict("last_admin", "")
	errUserNotFound = httpx.NotFound("user_not_found")
)

// deleteUser deletes the user and all their data in a transaction (DELETE /auth/me and the
// admins' DELETE /admin/users/{id}). It does not let the last active admin be deleted, and
// answers errUserNotFound if the user does not exist.
func (s *Service) deleteUser(ctx context.Context, userID int64) error {
	return pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		if err := checkNotLastAdmin(ctx, q, userID); err != nil {
			return err
		}
		rows, err := q.DeleteUser(ctx, userID)
		if _, ok := db.IsForeignKeyViolation(err); ok {
			return httpx.Conflict("account_has_data", "")
		}
		if err != nil {
			return fmt.Errorf("delete user %d: %w", userID, err)
		}
		if rows == 0 {
			return errUserNotFound
		}
		return nil
	})
}

// checkNotLastAdmin answers errLastAdmin if the user is the only active admin (with the admin role
// and not suspended; one who still lacks two-factor authentication counts too). It locks the
// active admins' rows until the end of the transaction q belongs to: that way two admins deleting
// their accounts, or demoting or suspending each other, at once cannot leave the API without any.
func checkNotLastAdmin(ctx context.Context, q *store.Queries, userID int64) error {
	admins, err := q.LockAdmins(ctx)
	if err != nil {
		return fmt.Errorf("find admins: %w", err)
	}
	if len(admins) == 1 && slices.Contains(admins, userID) {
		return errLastAdmin
	}
	return nil
}
