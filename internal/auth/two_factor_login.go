package auth

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/NoeOnDev/go-api-base/internal/httpx"
	"github.com/NoeOnDev/go-api-base/internal/store"
)

// startTwoFactorChallenge es el primer paso del login con 2FA: la contraseña ya fue correcta,
// así que se crea un desafío que vence en 5 minutos y se envía en una cookie HttpOnly.
func (s *Service) startTwoFactorChallenge(w http.ResponseWriter, r *http.Request, userID int64) error {
	token, err := s.issueToken(r.Context(), userID, purposeTwoFactorLogin, twoFactorLoginTTL)
	if err != nil {
		return err
	}
	s.setChallengeCookie(w, token, time.Now().Add(twoFactorLoginTTL))
	httpx.Write(w, http.StatusAccepted, map[string]bool{"two_factor_required": true})
	return nil
}

var errNoChallenge = httpx.Unauthorized("el inicio de sesión venció; volvé a ingresar tu email y contraseña")

type challengeInput struct {
	Code         string `json:"code"`
	RecoveryCode string `json:"recovery_code"`
}

// twoFactorChallenge responde POST /auth/two-factor/challenge con el código de la app
// ({"code"}) o un código de recuperación ({"recovery_code"}). Si es correcto, crea la sesión.
// Un código incorrecto no gasta el desafío (el límite de intentos frena la fuerza bruta).
func (s *Service) twoFactorChallenge(w http.ResponseWriter, r *http.Request) error {
	c, err := r.Cookie(ChallengeCookieName)
	if err != nil {
		return errNoChallenge
	}
	tokenHash := hashToken(c.Value)

	userID, err := s.q.GetUserTokenUser(r.Context(), store.GetUserTokenUserParams{TokenHash: tokenHash, Purpose: purposeTwoFactorLogin})
	if errors.Is(err, pgx.ErrNoRows) {
		return errNoChallenge
	}
	if err != nil {
		return fmt.Errorf("buscar desafío del 2FA: %w", err)
	}

	var in challengeInput
	if err := httpx.Decode(w, r, &in); err != nil {
		return err
	}
	in.Code = strings.TrimSpace(in.Code)
	in.RecoveryCode = strings.TrimSpace(in.RecoveryCode)
	if (in.Code == "") == (in.RecoveryCode == "") {
		return httpx.Validation(map[string]string{"code": "enviá el código de la app o un código de recuperación"})
	}

	stored, err := s.q.GetUserByID(r.Context(), userID)
	if err != nil {
		return fmt.Errorf("buscar usuario %d: %w", userID, err)
	}

	if in.Code != "" {
		if err := s.useTOTPCode(r, stored, in.Code); err != nil {
			return err
		}
	} else {
		ok, err := s.useRecoveryCode(r.Context(), userID, in.RecoveryCode)
		if err != nil {
			return err
		}
		if !ok {
			return httpx.Validation(map[string]string{"recovery_code": "el código no es válido o ya se usó"})
		}
	}

	if err := s.q.DeleteUserToken(r.Context(), tokenHash); err != nil {
		return fmt.Errorf("borrar desafío del 2FA: %w", err)
	}
	if err := s.StartSession(r.Context(), w, userID); err != nil {
		return err
	}
	s.clearChallengeCookie(w)

	httpx.Write(w, http.StatusOK, toUser(stored))
	return nil
}

// useTOTPCode valida el código de la app y registra su paso. Un código ya usado (por ejemplo,
// interceptado de un login anterior) se rechaza aunque siga dentro de su ventana de tiempo.
func (s *Service) useTOTPCode(r *http.Request, u store.User, code string) error {
	if u.TOTPEnabledAt == nil {
		return errWrongCode
	}
	step, ok, err := s.checkTOTP(u, code)
	if err != nil {
		return err
	}
	if !ok {
		return errWrongCode
	}

	rows, err := s.q.UseTOTPStep(r.Context(), store.UseTOTPStepParams{ID: u.ID, TOTPLastStep: &step})
	if err != nil {
		return fmt.Errorf("registrar código del 2FA: %w", err)
	}
	if rows == 0 {
		return errWrongCode // el código ya se usó
	}
	return nil
}
