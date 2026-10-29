package auth

import (
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
