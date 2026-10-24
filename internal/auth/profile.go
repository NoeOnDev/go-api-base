package auth

import (
	"fmt"
	"net/http"
	"strings"

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
	stored, err := s.q.GetUserByID(r.Context(), user.ID)
	if err != nil {
		return fmt.Errorf("buscar usuario %d: %w", user.ID, err)
	}

	ok, err := VerifyPassword(in.CurrentPassword, stored.PasswordHash)
	if err != nil {
		return fmt.Errorf("verificar contraseña del usuario %d: %w", user.ID, err)
	}
	if !ok {
		return httpx.Validation(map[string]string{"current_password": "es incorrecta"})
	}

	if err := s.setPassword(r, user.ID, in.Password); err != nil {
		return err
	}

	// Se cierran las demás sesiones; la de esta petición sigue activa.
	c, err := r.Cookie(CookieName)
	if err != nil {
		return fmt.Errorf("leer la cookie de sesión: %w", err)
	}
	if err := s.q.DeleteOtherSessions(r.Context(), store.DeleteOtherSessionsParams{
		UserID:    user.ID,
		TokenHash: hashToken(c.Value),
	}); err != nil {
		return fmt.Errorf("cerrar otras sesiones: %w", err)
	}

	httpx.Write(w, http.StatusNoContent, nil)
	return nil
}

// setPassword hashea y guarda una contraseña nueva.
func (s *Service) setPassword(r *http.Request, userID int64, password string) error {
	hash, err := HashPassword(password)
	if err != nil {
		return fmt.Errorf("hashear contraseña: %w", err)
	}
	if err := s.q.UpdateUserPassword(r.Context(), store.UpdateUserPasswordParams{ID: userID, PasswordHash: hash}); err != nil {
		return fmt.Errorf("guardar contraseña del usuario %d: %w", userID, err)
	}
	return nil
}
