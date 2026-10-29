package auth

import (
	"context"
	"fmt"
	"net/http"

	"github.com/NoeOnDev/go-api-base/internal/httpx"
)

// sendVerificationEmail envía el enlace para confirmar el email. Invalida los enlaces anteriores.
func (s *Service) sendVerificationEmail(ctx context.Context, u User) error {
	token, err := s.issueToken(ctx, u.ID, purposeEmailVerification, emailVerificationTTL, nil)
	if err != nil {
		return err
	}
	return s.sendEmail(ctx, u.Email, "Confirmá tu email", "verify_email.txt", emailData{
		Name:    u.Name,
		Link:    s.frontendLink("/verify-email", token),
		Expires: "24 horas",
	})
}

type verifyEmailInput struct {
	Token string `json:"token"`
}

// verifyEmail responde POST /auth/email/verify con el token del enlace. Es pública: el token
// alcanza para identificar al usuario, así funciona aunque abra el enlace en otro dispositivo.
func (s *Service) verifyEmail(w http.ResponseWriter, r *http.Request) error {
	var in verifyEmailInput
	if err := httpx.Decode(w, r, &in); err != nil {
		return err
	}

	userID, err := consumeToken(r.Context(), s.q, in.Token, purposeEmailVerification)
	if err != nil {
		return err
	}
	if err := s.q.MarkEmailVerified(r.Context(), userID); err != nil {
		return fmt.Errorf("marcar email verificado: %w", err)
	}

	httpx.Write(w, http.StatusNoContent, nil)
	return nil
}

// resendVerification responde POST /auth/email/resend: envía otro enlace al usuario de la sesión.
// Si el email ya está verificado no envía nada y responde 204. Después de varios envíos
// (ver allowEmail) responde 429: el que pide es el propio usuario, así que no hay nada que ocultar.
func (s *Service) resendVerification(w http.ResponseWriter, r *http.Request) error {
	u := MustUser(r.Context())
	if u.EmailVerified() {
		httpx.Write(w, http.StatusNoContent, nil)
		return nil
	}
	allowed, err := s.allowEmail(r.Context(), purposeEmailVerification, u.ID)
	if err != nil {
		return err
	}
	if !allowed {
		w.Header().Set("Retry-After", s.emailLimiter.RetryAfter())
		return &httpx.Error{
			Status:  http.StatusTooManyRequests,
			Code:    "too_many_requests",
			Message: "ya te enviamos varios enlaces; revisá tu correo o esperá unos minutos",
		}
	}

	if err := s.sendVerificationEmail(r.Context(), u); err != nil {
		return err
	}

	httpx.Write(w, http.StatusAccepted, map[string]string{
		"message": "Te enviamos un enlace nuevo para confirmar tu email.",
	})
	return nil
}
