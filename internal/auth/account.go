package auth

import (
	"context"
	"fmt"

	"github.com/NoeOnDev/go-api-base/internal/httpx"
	"github.com/NoeOnDev/go-api-base/internal/store"
)

// Ayudas que comparten varias funciones de la cuenta (perfil, sesiones, 2FA, cambio de email,
// borrar la cuenta). Están acá y no en el archivo de una función para que quitar una (ver "Quitar
// funciones" en docs/auth.md) no rompa las demás.

// passwordInput es el cuerpo de las acciones que solo piden la contraseña para confirmar.
type passwordInput struct {
	Password string `json:"password"`
}

// checkPassword confirma la contraseña actual del usuario antes de una acción sensible.
// Si no coincide, devuelve un error de validación en el campo indicado.
func (s *Service) checkPassword(ctx context.Context, userID int64, password, field string) error {
	if password == "" {
		return httpx.Validation(map[string]string{field: "es obligatoria"})
	}
	stored, err := s.q.GetUserByID(ctx, userID)
	if err != nil {
		return fmt.Errorf("buscar usuario %d: %w", userID, err)
	}
	ok, err := VerifyPassword(ctx, password, stored.PasswordHash)
	if err != nil {
		return fmt.Errorf("verificar contraseña del usuario %d: %w", userID, err)
	}
	if !ok {
		return httpx.Validation(map[string]string{field: "es incorrecta"})
	}
	return nil
}

// savePassword guarda el hash de una contraseña nueva con las consultas q (pueden ser las de
// una transacción). También borra lo que se obtuvo con la contraseña anterior, que pudo haber
// conocido otra persona: los desafíos del 2FA pendientes y el cambio de email pendiente (con
// él, alguien se quedaría con el email y, desde ahí, con la cuenta).
func savePassword(ctx context.Context, q *store.Queries, userID int64, hash string) error {
	if err := q.UpdateUserPassword(ctx, store.UpdateUserPasswordParams{ID: userID, PasswordHash: hash}); err != nil {
		return fmt.Errorf("guardar contraseña del usuario %d: %w", userID, err)
	}
	if err := q.DeleteUserTokens(ctx, store.DeleteUserTokensParams{UserID: userID, Purpose: purposeTwoFactorLogin}); err != nil {
		return fmt.Errorf("borrar desafíos del 2FA pendientes: %w", err)
	}
	if err := q.DeleteUserTokens(ctx, store.DeleteUserTokensParams{UserID: userID, Purpose: purposeEmailChange}); err != nil {
		return fmt.Errorf("borrar cambio de email pendiente: %w", err)
	}
	return nil
}
