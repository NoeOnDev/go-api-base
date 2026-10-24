package auth

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/NoeOnDev/go-api-base/internal/httpx"
	"github.com/NoeOnDev/go-api-base/internal/secretbox"
	"github.com/NoeOnDev/go-api-base/internal/store"
	"github.com/NoeOnDev/go-api-base/internal/totp"
)

// errNoBox indica que falta ENCRYPTION_KEY: sin ella no se puede guardar el secreto del 2FA.
var errNoBox = errors.New("2FA no disponible: falta configurar ENCRYPTION_KEY")

func (s *Service) box() (*secretbox.Box, error) {
	if s.opts.Box == nil {
		return nil, errNoBox
	}
	return s.opts.Box, nil
}

var (
	errTwoFactorEnabled    = httpx.Conflict("el 2FA ya está activo", "")
	errTwoFactorNotEnabled = httpx.Conflict("el 2FA no está activo", "")
	errSetupFirst          = httpx.Conflict("primero pedí el código QR con POST /auth/two-factor/setup", "")
	errWrongCode           = httpx.Validation(map[string]string{"code": "el código no es correcto"})
)

type setupResponse struct {
	// Secret es el secreto en base32, para cargarlo a mano si no se puede escanear el QR.
	Secret string `json:"secret"`
	// OTPAuthURL es lo que el frontend convierte en código QR.
	OTPAuthURL string `json:"otpauth_url"`
}

// setupTwoFactor responde POST /auth/two-factor/setup: genera un secreto nuevo y lo guarda
// cifrado, todavía sin activar. El 2FA se activa recién con enable, cuando el usuario demuestra
// que su app genera códigos correctos; así nadie queda bloqueado por un QR mal escaneado.
// Llamarlo de nuevo reemplaza el secreto pendiente.
func (s *Service) setupTwoFactor(w http.ResponseWriter, r *http.Request) error {
	user := MustUser(r.Context())
	if user.TwoFactorEnabled {
		return errTwoFactorEnabled
	}

	box, err := s.box()
	if err != nil {
		return err
	}

	secret, err := totp.GenerateSecret()
	if err != nil {
		return err
	}
	encrypted, err := box.Encrypt([]byte(secret))
	if err != nil {
		return fmt.Errorf("cifrar secreto del 2FA: %w", err)
	}

	rows, err := s.q.SetPendingTOTPSecret(r.Context(), store.SetPendingTOTPSecretParams{ID: user.ID, TOTPSecret: &encrypted})
	if err != nil {
		return fmt.Errorf("guardar secreto del 2FA: %w", err)
	}
	if rows == 0 {
		return errTwoFactorEnabled // se activó en otra petición al mismo tiempo
	}

	httpx.Write(w, http.StatusOK, setupResponse{
		Secret:     secret,
		OTPAuthURL: totp.URI(s.opts.Issuer, user.Email, secret),
	})
	return nil
}

type codeInput struct {
	Code string `json:"code"`
}

type recoveryCodesResponse struct {
	// RecoveryCodes se muestran una sola vez: el usuario tiene que guardarlos.
	RecoveryCodes []string `json:"recovery_codes"`
}

// enableTwoFactor responde POST /auth/two-factor/enable con un código de la app. Si es correcto,
// activa el 2FA, devuelve los códigos de recuperación y cierra las demás sesiones.
func (s *Service) enableTwoFactor(w http.ResponseWriter, r *http.Request) error {
	var in codeInput
	if err := httpx.Decode(w, r, &in); err != nil {
		return err
	}

	user := MustUser(r.Context())
	stored, err := s.q.GetUserByID(r.Context(), user.ID)
	if err != nil {
		return fmt.Errorf("buscar usuario %d: %w", user.ID, err)
	}
	if stored.TOTPEnabledAt != nil {
		return errTwoFactorEnabled
	}
	if stored.TOTPSecret == nil {
		return errSetupFirst
	}

	step, ok, err := s.checkTOTP(stored, in.Code)
	if err != nil {
		return err
	}
	if !ok {
		return errWrongCode
	}

	rows, err := s.q.EnableTOTP(r.Context(), store.EnableTOTPParams{ID: user.ID, TOTPLastStep: &step})
	if err != nil {
		return fmt.Errorf("activar 2FA: %w", err)
	}
	if rows == 0 {
		return errTwoFactorEnabled
	}

	codes, err := s.replaceRecoveryCodes(r.Context(), user.ID)
	if err != nil {
		return err
	}
	if err := s.endOtherSessions(r, user.ID); err != nil {
		return err
	}

	httpx.Write(w, http.StatusOK, recoveryCodesResponse{RecoveryCodes: codes})
	return nil
}

type passwordInput struct {
	Password string `json:"password"`
}

// disableTwoFactor responde POST /auth/two-factor/disable. Pide la contraseña, borra el secreto
// y los códigos de recuperación, y cierra las demás sesiones.
func (s *Service) disableTwoFactor(w http.ResponseWriter, r *http.Request) error {
	var in passwordInput
	if err := httpx.Decode(w, r, &in); err != nil {
		return err
	}

	user := MustUser(r.Context())
	if err := s.checkPassword(r.Context(), user.ID, in.Password, "password"); err != nil {
		return err
	}

	if err := s.q.DisableTOTP(r.Context(), user.ID); err != nil {
		return fmt.Errorf("desactivar 2FA: %w", err)
	}
	if err := s.q.DeleteRecoveryCodes(r.Context(), user.ID); err != nil {
		return fmt.Errorf("borrar códigos de recuperación: %w", err)
	}
	if err := s.endOtherSessions(r, user.ID); err != nil {
		return err
	}

	httpx.Write(w, http.StatusNoContent, nil)
	return nil
}

// regenerateRecoveryCodes responde POST /auth/two-factor/recovery-codes. Pide la contraseña
// y reemplaza los códigos: los anteriores dejan de servir.
func (s *Service) regenerateRecoveryCodes(w http.ResponseWriter, r *http.Request) error {
	var in passwordInput
	if err := httpx.Decode(w, r, &in); err != nil {
		return err
	}

	user := MustUser(r.Context())
	if err := s.checkPassword(r.Context(), user.ID, in.Password, "password"); err != nil {
		return err
	}
	if !user.TwoFactorEnabled {
		return errTwoFactorNotEnabled
	}

	codes, err := s.replaceRecoveryCodes(r.Context(), user.ID)
	if err != nil {
		return err
	}

	httpx.Write(w, http.StatusOK, recoveryCodesResponse{RecoveryCodes: codes})
	return nil
}

// checkTOTP descifra el secreto del usuario y comprueba el código en este momento.
func (s *Service) checkTOTP(u store.User, code string) (step int64, ok bool, err error) {
	box, err := s.box()
	if err != nil {
		return 0, false, err
	}
	if u.TOTPSecret == nil {
		return 0, false, nil
	}
	secret, err := box.Decrypt(*u.TOTPSecret)
	if err != nil {
		return 0, false, fmt.Errorf("descifrar secreto del 2FA del usuario %d: %w", u.ID, err)
	}
	return totp.Validate(string(secret), code, time.Now())
}
