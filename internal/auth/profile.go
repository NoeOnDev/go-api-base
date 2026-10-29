package auth

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/NoeOnDev/go-api-base/internal/httpx"
	"github.com/NoeOnDev/go-api-base/internal/i18n"
	"github.com/NoeOnDev/go-api-base/internal/store"
	"github.com/NoeOnDev/go-api-base/internal/validate"
)

type profileInput struct {
	Name string `json:"name"`
	// Locale es opcional: si no viene, el idioma no cambia.
	Locale *string `json:"locale"`
}

// updateProfile responde PUT /auth/me: cambia el nombre del usuario y, si viene, su idioma
// (uno de los disponibles; ver docs/i18n.md). El email no se cambia acá: cambiarlo exige
// verificar la nueva dirección.
func (s *Service) updateProfile(w http.ResponseWriter, r *http.Request) error {
	var in profileInput
	if err := httpx.Decode(w, r, &in); err != nil {
		return err
	}
	in.Name = strings.TrimSpace(in.Name)
	fields := httpx.Fields{}
	if msg := validate.Required(in.Name, maxNameLen); !msg.IsZero() {
		fields["name"] = msg
	}
	if in.Locale != nil {
		if locale, ok := s.catalog.Lookup(*in.Locale); ok {
			in.Locale = &locale // escrito como el catálogo ("es-mx" es "es-MX")
		} else {
			fields["locale"] = i18n.M("validation.unsupported_locale", "locales", strings.Join(s.catalog.Locales(), ", "))
		}
	}
	if len(fields) > 0 {
		return httpx.Validation(fields)
	}

	u, err := s.q.UpdateUserProfile(r.Context(), store.UpdateUserProfileParams{
		ID:     MustUser(r.Context()).ID,
		Name:   in.Name,
		Locale: in.Locale,
	})
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

	fields := httpx.Fields{}
	if in.CurrentPassword == "" {
		fields["current_password"] = i18n.M("validation.required")
	}
	if msg := validatePassword(in.Password); !msg.IsZero() {
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
	s.notify(r.Context(), user.ID, NotifyPasswordChanged, nil)

	httpx.Write(w, http.StatusNoContent, nil)
	return nil
}
