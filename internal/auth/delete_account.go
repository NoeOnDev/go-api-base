package auth

import (
	"fmt"
	"net/http"

	"github.com/jackc/pgx/v5"

	"github.com/NoeOnDev/go-api-base/internal/db"
	"github.com/NoeOnDev/go-api-base/internal/httpx"
)

var errLastAdmin = httpx.Conflict("no podés borrar la única cuenta de administrador; nombrá otro admin antes", "")

// deleteAccount responde DELETE /auth/me con {password}: borra la cuenta del usuario y todos sus
// datos (sesiones, tokens, notas, notificaciones…, por ON DELETE CASCADE). Pide la contraseña,
// como cualquier acción que no se puede deshacer.
//
// No deja borrar al único admin, para que nadie deje la API sin administrador por error.
// Si una tabla de un proyecto referencia al usuario sin CASCADE (con RESTRICT), el borrado
// falla y responde 409: cada tabla de datos del usuario debería usar CASCADE o SET NULL.
func (s *Service) deleteAccount(w http.ResponseWriter, r *http.Request) error {
	var in passwordInput
	if err := httpx.Decode(w, r, &in); err != nil {
		return err
	}
	user := MustUser(r.Context())
	if err := s.checkPassword(r.Context(), user.ID, in.Password, "password"); err != nil {
		return err
	}

	err := pgx.BeginFunc(r.Context(), s.db, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		if user.IsAdmin() {
			// Bloquea las filas de los admins: dos admins que se borran a la vez no pueden
			// dejar la API sin ninguno.
			admins, err := q.LockAdmins(r.Context())
			if err != nil {
				return fmt.Errorf("buscar admins: %w", err)
			}
			if len(admins) <= 1 {
				return errLastAdmin
			}
		}
		if _, err := q.DeleteUser(r.Context(), user.ID); err != nil {
			if _, ok := db.IsForeignKeyViolation(err); ok {
				return httpx.Conflict("la cuenta tiene datos que impiden borrarla; contactá al equipo", "")
			}
			return fmt.Errorf("borrar usuario %d: %w", user.ID, err)
		}
		return nil
	})
	if err != nil {
		return err
	}

	// Sus sesiones ya no existen: se cortan sus streams y se borra la cookie.
	s.sessionsEnded(r.Context(), user.ID, "")
	s.clearSessionCookie(w)

	// El último correo, por si no fue el dueño. Si no sale, la cuenta igual quedó borrada.
	if err := s.sendEmail(r.Context(), user.Email, "Tu cuenta fue eliminada", "account_deleted.txt", emailData{Name: user.Name}); err != nil {
		httpx.LoggerFrom(r.Context()).Error("no se pudo enviar el correo de cuenta eliminada", "user_id", user.ID, "error", err)
	}
	httpx.LoggerFrom(r.Context()).Info("cuenta eliminada", "user_id", user.ID)

	httpx.Write(w, http.StatusNoContent, nil)
	return nil
}
