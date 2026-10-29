package auth

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/NoeOnDev/go-api-base/internal/db"
	"github.com/NoeOnDev/go-api-base/internal/httpx"
	"github.com/NoeOnDev/go-api-base/internal/i18n"
	"github.com/NoeOnDev/go-api-base/internal/store"
)

type emailChangeInput struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

var errEmailTaken = httpx.Conflict("email_taken", "email")

// requestEmailChange responde PUT /auth/email con {email, password}: pide cambiar el email.
// El email no cambia todavía: se envía un enlace a la dirección nueva, y cambia recién cuando
// se confirma (POST /auth/email/confirm). Así el usuario demuestra que la dirección es suya.
//
// Pide la contraseña: si no, alguien con una sesión robada cambiaría el email, después
// recuperaría la contraseña con él y se quedaría con la cuenta. Además se le avisa al email
// actual, por si el pedido no lo hizo el dueño.
func (s *Service) requestEmailChange(w http.ResponseWriter, r *http.Request) error {
	var in emailChangeInput
	if err := httpx.Decode(w, r, &in); err != nil {
		return err
	}
	newEmail := strings.ToLower(strings.TrimSpace(in.Email))
	if msg := validateEmail(newEmail); !msg.IsZero() {
		return httpx.Validation(httpx.Fields{"email": msg})
	}

	user := MustUser(r.Context())
	if err := s.checkPassword(r.Context(), user.ID, in.Password, "password"); err != nil {
		return err
	}
	if newEmail == user.Email {
		return httpx.Validation(httpx.Fields{"email": i18n.M("validation.same_email")})
	}
	// Igual que el registro, indica si el email está en uso: si no, el usuario no sabría por qué
	// nunca se confirma.
	if _, err := s.q.GetUserByEmail(r.Context(), newEmail); err == nil {
		return errEmailTaken
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("buscar email: %w", err)
	}

	allowed, err := s.allowEmail(r.Context(), purposeEmailChange, user.ID)
	if err != nil {
		return err
	}
	if !allowed {
		w.Header().Set("Retry-After", s.emailLimiter.RetryAfter())
		return errTooManyEmails
	}

	token, err := s.issueToken(r.Context(), user.ID, purposeEmailChange, emailChangeTTL, &newEmail)
	if err != nil {
		return err
	}
	if err := s.sendEmail(r.Context(), newEmail, user.Locale, "email_change.txt", emailData{
		Name:    user.Name,
		Link:    s.frontendLink("/confirm-email", token),
		Expires: s.expiresIn(user.Locale, emailChangeTTL),
	}); err != nil {
		return err
	}
	if err := s.sendEmail(r.Context(), user.Email, user.Locale, "email_change_notice.txt", emailData{
		Name:  user.Name,
		Email: newEmail,
	}); err != nil {
		return err
	}

	httpx.Write(w, http.StatusAccepted, map[string]string{
		"message": i18n.FromContext(r.Context()).T("messages.email_change_sent", "email", newEmail),
	})
	return nil
}

type confirmEmailInput struct {
	Token string `json:"token"`
}

// confirmEmailChange responde POST /auth/email/confirm con el token del enlace. Es pública, como
// la verificación: el token identifica al usuario, así funciona aunque abra el enlace en otro
// dispositivo. Cambia el email y lo deja verificado.
func (s *Service) confirmEmailChange(w http.ResponseWriter, r *http.Request) error {
	var in confirmEmailInput
	if err := httpx.Decode(w, r, &in); err != nil {
		return err
	}
	if in.Token == "" {
		return errInvalidToken
	}

	// Todo o nada: si el email nuevo ya lo tomó otra cuenta, el enlace sigue sin usarse.
	var userID int64
	var oldEmail, newEmail string
	err := pgx.BeginFunc(r.Context(), s.db, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		row, err := q.ConsumeEmailChangeToken(r.Context(), hashToken(in.Token))
		if errors.Is(err, pgx.ErrNoRows) {
			return errInvalidToken
		}
		if err != nil {
			return fmt.Errorf("consumir token: %w", err)
		}
		before, err := q.GetUserByID(r.Context(), row.UserID)
		if err != nil {
			return fmt.Errorf("buscar usuario %d: %w", row.UserID, err)
		}
		if _, err := q.UpdateUserEmail(r.Context(), store.UpdateUserEmailParams{ID: row.UserID, Email: row.Email}); err != nil {
			if _, ok := db.IsUniqueViolation(err); ok {
				return errEmailTaken
			}
			return fmt.Errorf("cambiar email del usuario %d: %w", row.UserID, err)
		}
		// Los enlaces de recuperación pendientes se enviaron al email anterior, que ya no es de la
		// cuenta: si el usuario lo cambió porque perdió ese correo, quien lo tenga no puede usarlos.
		if err := q.DeleteUserTokens(r.Context(), store.DeleteUserTokensParams{UserID: row.UserID, Purpose: purposePasswordReset}); err != nil {
			return fmt.Errorf("borrar enlaces de recuperación pendientes: %w", err)
		}
		userID, oldEmail, newEmail = row.UserID, before.Email, row.Email
		return nil
	})
	if err != nil {
		return err
	}

	// El límite de intentos de login del email viejo ya no corresponde a nadie.
	s.resetLimit(r.Context(), s.loginLimiter, loginLimitKey(oldEmail))
	s.notify(r.Context(), userID, NotifyEmailChanged, map[string]any{"email": newEmail})

	httpx.Write(w, http.StatusNoContent, nil)
	return nil
}
