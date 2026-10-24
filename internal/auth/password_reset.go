package auth

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/NoeOnDev/go-api-base/internal/httpx"
)

type forgotPasswordInput struct {
	Email string `json:"email"`
}

// forgotMessage es la respuesta a POST /auth/password/forgot, exista o no el email.
var forgotMessage = map[string]string{
	"message": "Si el email está registrado, te enviamos un enlace para elegir una contraseña nueva.",
}

// forgotPassword responde POST /auth/password/forgot. Si el email existe, envía un enlace de un
// solo uso para elegir una contraseña nueva. Responde siempre lo mismo (202), exista o no,
// para no revelar qué emails están registrados; el correo se envía en segundo plano.
//
// Si ya se le enviaron varios enlaces al usuario (ver allowEmail), no envía otro ni invalida
// el anterior, pero responde lo mismo: así nadie puede llenarle la bandeja ni anularle el enlace.
func (s *Service) forgotPassword(w http.ResponseWriter, r *http.Request) error {
	var in forgotPasswordInput
	if err := httpx.Decode(w, r, &in); err != nil {
		return err
	}
	email := strings.ToLower(strings.TrimSpace(in.Email))
	if msg := validateEmail(email); msg != "" {
		return httpx.Validation(map[string]string{"email": msg})
	}

	u, err := s.q.GetUserByEmail(r.Context(), email)
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Write(w, http.StatusAccepted, forgotMessage)
		return nil
	}
	if err != nil {
		return fmt.Errorf("buscar usuario: %w", err)
	}
	if !s.allowEmail(purposePasswordReset, u.ID) {
		httpx.LoggerFrom(r.Context()).Warn("recuperación no enviada: límite de correos del usuario", "user_id", u.ID)
		httpx.Write(w, http.StatusAccepted, forgotMessage)
		return nil
	}

	token, err := s.issueToken(r.Context(), u.ID, purposePasswordReset, passwordResetTTL)
	if err != nil {
		return err
	}

	if err := s.sendEmail(r.Context(), u.Email, "Recuperá tu contraseña", "password_reset.txt", emailData{
		Name:    u.Name,
		Link:    s.frontendLink("/reset-password", token),
		Expires: "1 hora",
	}); err != nil {
		return err
	}

	httpx.Write(w, http.StatusAccepted, forgotMessage)
	return nil
}

type resetPasswordInput struct {
	Token    string `json:"token"`
	Password string `json:"password"`
}

// resetPassword responde POST /auth/password/reset con el token del enlace y la contraseña nueva.
// Cierra todas las sesiones del usuario (por si alguien más había entrado) y marca el email
// como verificado, porque el usuario demostró que recibe correos en esa dirección.
//
// También reinicia su límite de intentos del 2FA: si alguien con la contraseña vieja lo había
// agotado, el dueño no tiene que esperar para entrar con la nueva.
func (s *Service) resetPassword(w http.ResponseWriter, r *http.Request) error {
	var in resetPasswordInput
	if err := httpx.Decode(w, r, &in); err != nil {
		return err
	}

	// Primero se valida la contraseña: si no cumple las reglas, el token sigue sirviendo.
	if msg := validatePassword(in.Password); msg != "" {
		return httpx.Validation(map[string]string{"password": msg})
	}

	userID, err := s.consumeToken(r.Context(), in.Token, purposePasswordReset)
	if err != nil {
		return err
	}

	if err := s.setPassword(r, userID, in.Password); err != nil {
		return err
	}
	if err := s.q.MarkEmailVerified(r.Context(), userID); err != nil {
		return fmt.Errorf("marcar email verificado: %w", err)
	}
	if err := s.q.DeleteUserSessions(r.Context(), userID); err != nil {
		return fmt.Errorf("cerrar sesiones: %w", err)
	}
	s.twoFactorLimiter.Reset(twoFactorLimitKey(userID))

	httpx.Write(w, http.StatusNoContent, nil)
	return nil
}
