package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
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

var (
	errWrongRecoveryCode = httpx.Validation(map[string]string{"recovery_code": "el código no es válido o ya se usó"})
	errTwoFactorLocked   = &httpx.Error{
		Status:  http.StatusUnauthorized,
		Code:    "two_factor_locked",
		Message: "demasiados códigos incorrectos; volvé a ingresar tu email y contraseña",
	}
)

// Frenos contra la fuerza bruta del código, que hace alguien que ya tiene la contraseña:
//   - Cada desafío admite maxChallengeAttempts intentos; después hay que volver a poner la contraseña.
//   - Cada usuario tiene twoFactorLimitBurst intentos seguidos y luego uno cada twoFactorLimitEvery
//     (10 por hora), sin importar desde cuántas IPs lleguen. Con 3 códigos válidos en un millón,
//     son unas 240 pruebas por día: cerca de un 0,07 % de acertar.
//
// El límite por usuario está en memoria: alcanza para una sola instancia, como el límite por IP.
const (
	maxChallengeAttempts = 5
	twoFactorLimitEvery  = 6 * time.Minute
	twoFactorLimitBurst  = 10
)

// twoFactorLimitKey es la clave del usuario en el límite de intentos del 2FA.
func twoFactorLimitKey(userID int64) string {
	return "two_factor:" + strconv.FormatInt(userID, 10)
}

type challengeInput struct {
	Code         string `json:"code"`
	RecoveryCode string `json:"recovery_code"`
}

// twoFactorChallenge responde POST /auth/two-factor/challenge con el código de la app
// ({"code"}) o un código de recuperación ({"recovery_code"}). Si es correcto, crea la sesión.
// Un código incorrecto deja reintentar hasta maxChallengeAttempts veces; al agotarlos se borra
// el desafío y se le avisa al usuario por correo.
func (s *Service) twoFactorChallenge(w http.ResponseWriter, r *http.Request) error {
	c, err := r.Cookie(s.cookieName(ChallengeCookieName))
	if err != nil {
		return errNoChallenge
	}
	tokenHash := hashToken(c.Value)

	// Primero el cuerpo: un pedido mal armado no gasta intentos.
	var in challengeInput
	if err := httpx.Decode(w, r, &in); err != nil {
		return err
	}
	in.Code = strings.TrimSpace(in.Code)
	in.RecoveryCode = strings.TrimSpace(in.RecoveryCode)
	if (in.Code == "") == (in.RecoveryCode == "") {
		return httpx.Validation(map[string]string{"code": "enviá el código de la app o un código de recuperación"})
	}

	challenge := store.GetUserTokenUserParams{TokenHash: tokenHash, Purpose: purposeTwoFactorLogin, MaxAttempts: maxChallengeAttempts}
	userID, err := s.q.GetUserTokenUser(r.Context(), challenge)
	if errors.Is(err, pgx.ErrNoRows) {
		return errNoChallenge
	}
	if err != nil {
		return fmt.Errorf("buscar desafío del 2FA: %w", err)
	}

	if !s.twoFactorLimiter.Allow(twoFactorLimitKey(userID)) {
		httpx.LoggerFrom(r.Context()).Warn("límite de intentos del 2FA del usuario superado", "user_id", userID)
		w.Header().Set("Retry-After", strconv.Itoa(int(twoFactorLimitEvery.Seconds())))
		return httpx.TooManyRequests()
	}

	attempt, err := s.q.UseUserTokenAttempt(r.Context(), store.UseUserTokenAttemptParams(challenge))
	if errors.Is(err, pgx.ErrNoRows) {
		return errNoChallenge // venció o se agotó entre las dos consultas
	}
	if err != nil {
		return fmt.Errorf("registrar intento del 2FA: %w", err)
	}

	stored, err := s.q.GetUserByID(r.Context(), userID)
	if err != nil {
		return fmt.Errorf("buscar usuario %d: %w", userID, err)
	}

	if err := s.checkSecondFactor(r, stored, in); err != nil {
		if attempt >= maxChallengeAttempts && (errors.Is(err, errWrongCode) || errors.Is(err, errWrongRecoveryCode)) {
			return s.lockChallenge(w, r, stored, tokenHash)
		}
		return err
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

// checkSecondFactor comprueba el código de la app o el código de recuperación.
// Un código incorrecto devuelve errWrongCode o errWrongRecoveryCode.
func (s *Service) checkSecondFactor(r *http.Request, u store.User, in challengeInput) error {
	if in.Code != "" {
		return s.useTOTPCode(r, u, in.Code)
	}
	ok, err := s.useRecoveryCode(r.Context(), u.ID, in.RecoveryCode)
	if err != nil {
		return err
	}
	if !ok {
		return errWrongRecoveryCode
	}
	return nil
}

// lockChallenge corta el desafío después de demasiados códigos incorrectos: hay que volver a
// ingresar la contraseña. Quien llegó hasta acá la conoce, así que se le avisa al usuario.
func (s *Service) lockChallenge(w http.ResponseWriter, r *http.Request, u store.User, tokenHash []byte) error {
	if err := s.q.DeleteUserToken(r.Context(), tokenHash); err != nil {
		return fmt.Errorf("borrar desafío del 2FA: %w", err)
	}
	s.clearChallengeCookie(w)

	ip := httpx.ClientIP(r, s.opts.TrustedProxies)
	httpx.LoggerFrom(r.Context()).Warn("desafío del 2FA bloqueado por códigos incorrectos", "user_id", u.ID, "ip", ip)
	s.sendTwoFactorLockedEmail(r.Context(), u, ip)

	return errTwoFactorLocked
}

// sendTwoFactorLockedEmail avisa al usuario que alguien con su contraseña falló el código del 2FA.
// Si no se puede enviar, solo se registra: la respuesta al cliente no cambia.
func (s *Service) sendTwoFactorLockedEmail(ctx context.Context, u store.User, ip string) {
	log := httpx.LoggerFrom(ctx)
	if !s.allowEmail("two_factor_locked", u.ID) {
		log.Warn("aviso de 2FA bloqueado no enviado: límite de correos del usuario", "user_id", u.ID)
		return
	}
	err := s.sendEmail(ctx, u.Email, "Intento de inicio de sesión bloqueado", "two_factor_locked.txt", emailData{
		Name: u.Name,
		Link: s.opts.FrontendURL,
		When: time.Now().UTC().Format("02/01/2006 15:04") + " (UTC)",
		IP:   ip,
	})
	if err != nil {
		log.Error("no se pudo enviar el aviso de 2FA bloqueado", "user_id", u.ID, "error", err)
	}
}

// useTOTPCode valida el código de la app y registra su paso. Un código ya usado (por ejemplo,
// interceptado de un login anterior) se rechaza aunque siga dentro de su ventana de tiempo.
func (s *Service) useTOTPCode(r *http.Request, u store.User, code string) error {
	if u.TOTPEnabledAt == nil {
		return errWrongCode
	}
	step, ok, err := s.checkTOTP(r.Context(), u, code)
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
