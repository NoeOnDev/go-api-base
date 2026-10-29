package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/NoeOnDev/go-api-base/internal/httpx"
	"github.com/NoeOnDev/go-api-base/internal/store"
)

// Propósitos de los tokens de un solo uso que se envían por correo.
// Deben coincidir con el CHECK de la tabla user_tokens.
const (
	purposePasswordReset     = "password_reset"
	purposeEmailVerification = "email_verification"
	purposeEmailChange       = "email_change"
	purposeTwoFactorLogin    = "two_factor_login"
)

// Vigencia de cada tipo de token.
const (
	passwordResetTTL     = time.Hour
	emailVerificationTTL = 24 * time.Hour
	emailChangeTTL       = time.Hour
	twoFactorLoginTTL    = 5 * time.Minute
)

// errInvalidToken responde a un enlace inventado, vencido o ya usado. No distingue entre
// los casos: al usuario solo le sirve saber que tiene que pedir uno nuevo.
var errInvalidToken = httpx.Validation(map[string]string{
	"token": "el enlace no es válido o ya venció; pedí uno nuevo",
})

// issueToken crea un token de un solo uso para el usuario y lo devuelve para enviarlo por correo.
// Invalida los anteriores del mismo propósito: solo sirve el último enlace enviado. newEmail es
// el email nuevo para email_change; nil para los demás propósitos.
//
// Va en una transacción que bloquea la fila del usuario. La transacción sola no alcanza: con el
// aislamiento por defecto, dos pedidos simultáneos podrían borrar los dos "nada" y crear cada uno
// su token, y quedarían dos enlaces válidos. Con el bloqueo, el segundo espera al primero.
func (s *Service) issueToken(ctx context.Context, userID int64, purpose string, ttl time.Duration, newEmail *string) (string, error) {
	token, hash, err := newToken()
	if err != nil {
		return "", err
	}

	err = pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		if err := q.LockUser(ctx, userID); err != nil {
			return fmt.Errorf("bloquear usuario %d: %w", userID, err)
		}
		if err := q.DeleteUserTokens(ctx, store.DeleteUserTokensParams{UserID: userID, Purpose: purpose}); err != nil {
			return fmt.Errorf("borrar tokens anteriores: %w", err)
		}
		if err := q.CreateUserToken(ctx, store.CreateUserTokenParams{
			TokenHash: hash,
			UserID:    userID,
			Purpose:   purpose,
			ExpiresAt: time.Now().Add(ttl),
			Email:     newEmail,
		}); err != nil {
			return fmt.Errorf("crear token: %w", err)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	return token, nil
}

// consumeToken valida el token y lo borra en la misma operación, así que no se puede usar dos veces.
// Usa las consultas q, que pueden ser las de una transacción: si la transacción se deshace, el
// token vuelve a servir. Devuelve el usuario al que pertenece, o errInvalidToken.
func consumeToken(ctx context.Context, q *store.Queries, token, purpose string) (int64, error) {
	if token == "" {
		return 0, errInvalidToken
	}

	userID, err := q.ConsumeUserToken(ctx, store.ConsumeUserTokenParams{TokenHash: hashToken(token), Purpose: purpose})
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, errInvalidToken
	}
	if err != nil {
		return 0, fmt.Errorf("consumir token: %w", err)
	}
	return userID, nil
}
