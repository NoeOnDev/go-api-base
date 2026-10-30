package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/NoeOnDev/go-api-base/internal/httpx"
	"github.com/NoeOnDev/go-api-base/internal/i18n"
	"github.com/NoeOnDev/go-api-base/internal/store"
)

// Purposes of the single-use tokens sent by email.
// They must match the CHECK of the user_tokens table.
const (
	purposePasswordReset     = "password_reset"
	purposeEmailVerification = "email_verification"
	purposeEmailChange       = "email_change"
	purposeTwoFactorLogin    = "two_factor_login"
	purposeInvitation        = "invitation"
)

// How long each kind of token lasts.
const (
	passwordResetTTL     = time.Hour
	emailVerificationTTL = 24 * time.Hour
	emailChangeTTL       = time.Hour
	twoFactorLoginTTL    = 5 * time.Minute
	invitationTTL        = 7 * 24 * time.Hour
)

// errInvalidToken answers a made-up, expired or already used link. It does not tell the cases
// apart: the user only needs to know they have to ask for a new one.
var errInvalidToken = httpx.Validation(httpx.Fields{"token": i18n.M("validation.invalid_token")})

// issueToken creates a single-use token for the user and returns it to send it by email.
// It invalidates the previous ones of the same purpose: only the last link sent works. newEmail
// is the new email for email_change; nil for the other purposes.
//
// It runs in a transaction that locks the user's row. The transaction alone is not enough: with
// the default isolation, two simultaneous requests could both delete "nothing" and each create
// its token, leaving two valid links. With the lock, the second one waits for the first.
func (s *Service) issueToken(ctx context.Context, userID int64, purpose string, ttl time.Duration, newEmail *string) (string, error) {
	token, hash, err := newToken()
	if err != nil {
		return "", err
	}

	err = pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		if err := q.LockUser(ctx, userID); err != nil {
			return fmt.Errorf("lock user %d: %w", userID, err)
		}
		if err := q.DeleteUserTokens(ctx, store.DeleteUserTokensParams{UserID: userID, Purpose: purpose}); err != nil {
			return fmt.Errorf("delete the previous tokens: %w", err)
		}
		if err := q.CreateUserToken(ctx, store.CreateUserTokenParams{
			TokenHash: hash,
			UserID:    userID,
			Purpose:   purpose,
			ExpiresAt: time.Now().Add(ttl),
			Email:     newEmail,
		}); err != nil {
			return fmt.Errorf("create token: %w", err)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	return token, nil
}

// consumeToken validates the token and deletes it in the same operation, so it cannot be used
// twice. It uses the queries q, which can be those of a transaction: if the transaction rolls
// back, the token works again. It returns the user it belongs to, or errInvalidToken.
func consumeToken(ctx context.Context, q *store.Queries, token, purpose string) (int64, error) {
	if token == "" {
		return 0, errInvalidToken
	}

	userID, err := q.ConsumeUserToken(ctx, store.ConsumeUserTokenParams{TokenHash: hashToken(token), Purpose: purpose})
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, errInvalidToken
	}
	if err != nil {
		return 0, fmt.Errorf("consume the token: %w", err)
	}
	return userID, nil
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
