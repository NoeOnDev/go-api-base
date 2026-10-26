package auth

import (
	"context"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/NoeOnDev/go-api-base/internal/httpx"
	"github.com/NoeOnDev/go-api-base/internal/store"
	"github.com/NoeOnDev/go-api-base/internal/validate"
)

type profileInput struct {
	Name string `json:"name"`
}

// updateProfile responde PUT /auth/me: cambia el nombre del usuario.
// El email no se cambia acá: cambiarlo exige verificar la nueva dirección.
func (s *Service) updateProfile(w http.ResponseWriter, r *http.Request) error {
	var in profileInput
	if err := httpx.Decode(w, r, &in); err != nil {
		return err
	}
	in.Name = strings.TrimSpace(in.Name)
	if msg := validate.Required(in.Name, maxNameLen); msg != "" {
		return httpx.Validation(map[string]string{"name": msg})
	}

	u, err := s.q.UpdateUserName(r.Context(), store.UpdateUserNameParams{ID: MustUser(r.Context()).ID, Name: in.Name})
	if err != nil {
		return fmt.Errorf("actualizar perfil: %w", err)
	}

	httpx.Write(w, http.StatusOK, toUser(u))
	return nil
}

type changePasswordInput struct {
	CurrentPassword string `json:"current_password"`
	Password        string `json:"password"`
}

// changePassword responde PUT /auth/password. Pide la contraseña actual (para que alguien
// que encuentre la sesión abierta no pueda cambiarla) y cierra todas las demás sesiones
// del usuario, por si alguien había entrado con la contraseña anterior. La sesión actual sigue activa.
func (s *Service) changePassword(w http.ResponseWriter, r *http.Request) error {
	var in changePasswordInput
	if err := httpx.Decode(w, r, &in); err != nil {
		return err
	}

	fields := map[string]string{}
	if in.CurrentPassword == "" {
		fields["current_password"] = "es obligatoria"
	}
	if msg := validatePassword(in.Password); msg != "" {
		fields["password"] = msg
	}
	if len(fields) > 0 {
		return httpx.Validation(fields)
	}

	user := MustUser(r.Context())
	if err := s.checkPassword(r.Context(), user.ID, in.CurrentPassword, "current_password"); err != nil {
		return err
	}

	// El hash (argon2, lento) se calcula antes de abrir la transacción, para no tener una
	// conexión de la base ocupada mientras espera su turno.
	hash, err := HashPassword(r.Context(), in.Password)
	if err != nil {
		return fmt.Errorf("hashear contraseña: %w", err)
	}

	// La contraseña nueva y el cierre de las demás sesiones van juntos: o pasan los dos, o ninguno.
	var keep string
	err = pgx.BeginFunc(r.Context(), s.db, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		if err := savePassword(r.Context(), q, user.ID, hash); err != nil {
			return err
		}
		var err error
		keep, err = s.deleteOtherSessions(r, q, user.ID)
		return err
	})
	if err != nil {
		return err
	}
	s.sessionsEnded(r.Context(), user.ID, keep)
	s.notify(r.Context(), user.ID, NotifyPasswordChanged, "Se cambió la contraseña de tu cuenta. Si no fuiste vos, recuperala cuanto antes.")

	httpx.Write(w, http.StatusNoContent, nil)
	return nil
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

// endOtherSessions cierra todas las sesiones del usuario salvo la de esta petición, y avisa al
// SessionWatcher. Es para cuando no hay nada más que guardar junto (POST /auth/sessions/revoke-others);
// si lo hay, deleteOtherSessions dentro de la transacción y sessionsEnded después del commit.
func (s *Service) endOtherSessions(r *http.Request, userID int64) error {
	keep, err := s.deleteOtherSessions(r, s.q, userID)
	if err != nil {
		return err
	}
	s.sessionsEnded(r.Context(), userID, keep)
	return nil
}

// deleteOtherSessions borra todas las sesiones del usuario salvo la de esta petición, con las
// consultas q (pueden ser las de una transacción). Devuelve la clave de la sesión que sigue,
// para avisar con sessionsEnded después del commit.
func (s *Service) deleteOtherSessions(r *http.Request, q *store.Queries, userID int64) (string, error) {
	c, err := r.Cookie(s.cookieName(CookieName))
	if err != nil {
		return "", fmt.Errorf("leer la cookie de sesión: %w", err)
	}
	hash := hashToken(c.Value)
	if err := q.DeleteOtherSessions(r.Context(), store.DeleteOtherSessionsParams{
		UserID:    userID,
		TokenHash: hash,
	}); err != nil {
		return "", fmt.Errorf("cerrar otras sesiones: %w", err)
	}
	return hex.EncodeToString(hash), nil
}

// sessionsEnded avisa al SessionWatcher que se cerraron las sesiones del usuario salvo keep
// (vacío: todas), para cortar los streams abiertos con ellas. Va después del commit: si fuera
// antes, un stream cortado podría reconectarse con una sesión que todavía no se borró.
func (s *Service) sessionsEnded(ctx context.Context, userID int64, keep string) {
	if s.opts.SessionWatcher != nil {
		s.opts.SessionWatcher.UserSessionsEnded(ctx, userID, keep)
	}
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
