package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/NoeOnDev/go-api-base/internal/httpx"
	"github.com/NoeOnDev/go-api-base/internal/store"
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
	allowed, err := s.allowEmail(r.Context(), purposePasswordReset, u.ID)
	if err != nil {
		return err
	}
	if !allowed {
		httpx.LoggerFrom(r.Context()).Warn("recuperación no enviada: límite de correos del usuario", "user_id", u.ID)
		httpx.Write(w, http.StatusAccepted, forgotMessage)
		return nil
	}

	token, err := s.issueToken(r.Context(), u.ID, purposePasswordReset, passwordResetTTL, nil)
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
// También reinicia sus límites de intentos del login y del 2FA: si alguien los había agotado,
// el dueño no tiene que esperar para entrar con la contraseña nueva.
func (s *Service) resetPassword(w http.ResponseWriter, r *http.Request) error {
	var in resetPasswordInput
	if err := httpx.Decode(w, r, &in); err != nil {
		return err
	}

	// Primero se valida la contraseña: si no cumple las reglas, el token sigue sirviendo.
	if msg := validatePassword(in.Password); msg != "" {
		return httpx.Validation(map[string]string{"password": msg})
	}

	// Antes de calcular el hash (argon2, caro) se comprueba que el enlace sirva, sin gastarlo.
	// El hash se calcula fuera de la transacción, para no tener una conexión de la base
	// ocupada mientras espera su turno.
	if err := s.checkToken(r.Context(), in.Token, purposePasswordReset); err != nil {
		return err
	}
	hash, err := HashPassword(r.Context(), in.Password)
	if err != nil {
		return fmt.Errorf("hashear contraseña: %w", err)
	}

	// Todo o nada: si un paso falla, el enlace sigue sirviendo y nada cambió.
	var userID int64
	err = pgx.BeginFunc(r.Context(), s.db, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		id, err := consumeToken(r.Context(), q, in.Token, purposePasswordReset)
		if err != nil {
			return err
		}
		if err := savePassword(r.Context(), q, id, hash); err != nil {
			return err
		}
		if err := q.MarkEmailVerified(r.Context(), id); err != nil {
			return fmt.Errorf("marcar email verificado: %w", err)
		}
		if err := q.DeleteUserSessions(r.Context(), id); err != nil {
			return fmt.Errorf("cerrar sesiones: %w", err)
		}
		userID = id
		return nil
	})
	if err != nil {
		return err
	}
	s.sessionsEnded(r.Context(), userID, "")
	u, err := s.q.GetUserByID(r.Context(), userID)
	if err != nil {
		return fmt.Errorf("buscar usuario %d: %w", userID, err)
	}
	s.resetLimit(r.Context(), s.loginLimiter, loginLimitKey(u.Email))
	s.resetLimit(r.Context(), s.twoFactorLimiter, twoFactorLimitKey(userID))
	s.notify(r.Context(), userID, NotifyPasswordChanged, "Se cambió la contraseña de tu cuenta con un enlace de recuperación.")

	httpx.Write(w, http.StatusNoContent, nil)
	return nil
}

// checkToken comprueba que el token sea válido, sin gastarlo. Sirve para no hacer trabajo caro
// (como calcular un hash de argon2) con un enlace inventado o vencido; después hay que
// consumirlo con consumeToken, que lo vuelve a comprobar.
func (s *Service) checkToken(ctx context.Context, token, purpose string) error {
	if token == "" {
		return errInvalidToken
	}
	// Los tokens que se envían por correo no gastan intentos (attempts queda en 0): con un
	// máximo de 1, solo se exige que sigan sin usar.
	_, err := s.q.GetUserTokenUser(ctx, store.GetUserTokenUserParams{TokenHash: hashToken(token), Purpose: purpose, MaxAttempts: 1})
	if errors.Is(err, pgx.ErrNoRows) {
		return errInvalidToken
	}
	if err != nil {
		return fmt.Errorf("buscar token: %w", err)
	}
	return nil
}
