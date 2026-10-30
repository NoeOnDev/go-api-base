package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/NoeOnDev/go-api-base/internal/httpx"
	"github.com/NoeOnDev/go-api-base/internal/i18n"
	"github.com/NoeOnDev/go-api-base/internal/secretbox"
	"github.com/NoeOnDev/go-api-base/internal/store"
	"github.com/NoeOnDev/go-api-base/internal/totp"
)

// totpAAD is the context the two-factor secret is encrypted with: it binds it to the user, so a
// secret copied to another user's row cannot be decrypted.
func totpAAD(userID int64) []byte {
	return []byte("users.totp_secret:" + strconv.FormatInt(userID, 10))
}

var (
	errTwoFactorEnabled    = httpx.Conflict("two_factor_already_enabled", "")
	errTwoFactorNotEnabled = httpx.Conflict("two_factor_not_enabled", "")
	errSetupFirst          = httpx.Conflict("two_factor_setup_required", "")
	errSecretChanged       = httpx.Conflict("two_factor_secret_changed", "")
	errWrongCode           = httpx.Validation(httpx.Fields{"code": i18n.M("validation.wrong_code")})
)

type setupResponse struct {
	// Secret is the secret in base32, to enter it by hand if the QR code cannot be scanned.
	Secret string `json:"secret"`
	// OTPAuthURL is what the frontend turns into a QR code.
	OTPAuthURL string `json:"otpauth_url"`
}

// setupTwoFactor answers POST /auth/two-factor/setup: it generates a new secret and stores it
// encrypted, not enabled yet. Two-factor authentication is enabled only with enable, when the
// user proves their app generates correct codes; that way nobody gets locked out by a badly
// scanned QR code. Calling it again replaces the pending secret.
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
	encrypted, err := box.Encrypt([]byte(secret), totpAAD(user.ID))
	if err != nil {
		return fmt.Errorf("encrypt the two-factor secret: %w", err)
	}

	rows, err := s.q.SetPendingTOTPSecret(r.Context(), store.SetPendingTOTPSecretParams{ID: user.ID, TOTPSecret: &encrypted})
	if err != nil {
		return fmt.Errorf("store the two-factor secret: %w", err)
	}
	if rows == 0 {
		return errTwoFactorEnabled // it was enabled in another request at the same time
	}

	httpx.Write(w, http.StatusOK, setupResponse{
		Secret:     secret,
		OTPAuthURL: totp.URI(s.opts.Issuer, user.Email, secret),
	})
	return nil
}

type enableInput struct {
	Code     string `json:"code"`
	Password string `json:"password"`
}

type recoveryCodesResponse struct {
	// RecoveryCodes are shown only once: the user has to keep them.
	RecoveryCodes []string `json:"recovery_codes"`
}

// enableTwoFactor answers POST /auth/two-factor/enable with {code, password}. If both are
// correct, it enables two-factor authentication, returns the recovery codes and closes the
// other sessions.
//
// It asks for the password: otherwise someone with a stolen session would enable it with their
// own app and, as the other sessions close, lock the owner out (a password reset does not turn
// it off).
func (s *Service) enableTwoFactor(w http.ResponseWriter, r *http.Request) error {
	var in enableInput
	if err := httpx.Decode(w, r, &in); err != nil {
		return err
	}

	user := MustUser(r.Context())
	if err := s.checkPassword(r.Context(), user.ID, in.Password, "password"); err != nil {
		return err
	}

	stored, err := s.q.GetUserByID(r.Context(), user.ID)
	if err != nil {
		return fmt.Errorf("find user %d: %w", user.ID, err)
	}
	if stored.TOTPEnabledAt != nil {
		return errTwoFactorEnabled
	}
	if stored.TOTPSecret == nil {
		return errSetupFirst
	}

	// The secret is not encrypted again even if it uses an old key: EnableTOTP compares with the
	// secret that was read, and rotate-keys also encrypts the pending ones with the current key.
	step, ok, _, err := s.verifyTOTP(stored, in.Code)
	if err != nil {
		return err
	}
	if !ok {
		return errWrongCode
	}

	// Enabling, storing the recovery codes and closing the other sessions go together: if a step
	// fails, two-factor authentication stays off and the user can try again.
	var codes []string
	var keep string
	err = pgx.BeginFunc(r.Context(), s.db, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		rows, err := q.EnableTOTP(r.Context(), store.EnableTOTPParams{ID: user.ID, TOTPSecret: stored.TOTPSecret, TOTPLastStep: &step})
		if err != nil {
			return fmt.Errorf("enable two-factor authentication: %w", err)
		}
		if rows == 0 {
			// While the code was being verified, another request enabled it or asked for a new QR code.
			current, err := q.GetUserByID(r.Context(), user.ID)
			if err != nil {
				return fmt.Errorf("find user %d: %w", user.ID, err)
			}
			if current.TOTPEnabledAt != nil {
				return errTwoFactorEnabled
			}
			return errSecretChanged
		}
		if codes, err = s.replaceRecoveryCodes(r.Context(), q, user.ID); err != nil {
			return err
		}
		keep, err = s.deleteOtherSessions(r, q, user.ID)
		return err
	})
	if err != nil {
		return err
	}
	s.sessionsEnded(r.Context(), user.ID, keep)
	s.notify(r.Context(), user.ID, NotifyTwoFactorEnabled, nil)

	httpx.Write(w, http.StatusOK, recoveryCodesResponse{RecoveryCodes: codes})
	return nil
}

// disableInput is the body of POST /auth/two-factor/disable: the password and a second factor.
type disableInput struct {
	Password string `json:"password"`
	challengeInput
}

// disableTwoFactor answers POST /auth/two-factor/disable. It asks for the password and a second
// factor (the app code or a recovery code): with only the password, whoever has a stolen session
// and the password could turn it off and then log in without the phone. It deletes the secret and
// the recovery codes, and closes the other sessions.
func (s *Service) disableTwoFactor(w http.ResponseWriter, r *http.Request) error {
	var in disableInput
	if err := httpx.Decode(w, r, &in); err != nil {
		return err
	}
	in.normalize()
	if err := in.validate(); err != nil {
		return err
	}

	user := MustUser(r.Context())
	if err := s.checkPassword(r.Context(), user.ID, in.Password, "password"); err != nil {
		return err
	}

	stored, err := s.q.GetUserByID(r.Context(), user.ID)
	if err != nil {
		return fmt.Errorf("find user %d: %w", user.ID, err)
	}
	if stored.TOTPEnabledAt == nil {
		return errTwoFactorNotEnabled
	}

	// The codes share the per-user limit of the login's second step: guessing here does not give
	// more attempts.
	allowed, err := s.twoFactorLimiter.Allow(r.Context(), twoFactorLimitKey(user.ID))
	if err != nil {
		return err
	}
	if !allowed {
		httpx.LoggerFrom(r.Context()).Warn("the user's two-factor attempt limit exceeded", "user_id", user.ID)
		w.Header().Set("Retry-After", s.twoFactorLimiter.RetryAfter())
		return httpx.TooManyRequests()
	}
	if err := s.checkSecondFactor(r, stored, in.challengeInput); err != nil {
		return err
	}

	// All or nothing: if a step fails, two-factor authentication stays on with its codes and sessions.
	var keep string
	err = pgx.BeginFunc(r.Context(), s.db, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		if err := q.DisableTOTP(r.Context(), user.ID); err != nil {
			return fmt.Errorf("disable two-factor authentication: %w", err)
		}
		if err := q.DeleteRecoveryCodes(r.Context(), user.ID); err != nil {
			return fmt.Errorf("delete the recovery codes: %w", err)
		}
		var err error
		keep, err = s.deleteOtherSessions(r, q, user.ID)
		return err
	})
	if err != nil {
		return err
	}
	s.sessionsEnded(r.Context(), user.ID, keep)
	s.notify(r.Context(), user.ID, NotifyTwoFactorDisabled, nil)

	httpx.Write(w, http.StatusNoContent, nil)
	return nil
}

// regenerateRecoveryCodes answers POST /auth/two-factor/recovery-codes. It asks for the
// password and replaces the codes: the previous ones stop working.
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

	codes, err := s.replaceRecoveryCodes(r.Context(), s.q, user.ID)
	if err != nil {
		return err
	}

	httpx.Write(w, http.StatusOK, recoveryCodesResponse{RecoveryCodes: codes})
	return nil
}

// verifyTOTP decrypts the user's secret and checks the code at this moment. It also returns the
// decrypted secret, to encrypt it again if needed (see checkTOTP).
func (s *Service) verifyTOTP(u store.User, code string) (step int64, ok bool, secret []byte, err error) {
	if u.TOTPSecret == nil {
		return 0, false, nil, nil
	}
	box, err := s.box()
	if err != nil {
		return 0, false, nil, err
	}

	secret, err = box.Decrypt(*u.TOTPSecret, totpAAD(u.ID))
	if err != nil {
		return 0, false, nil, twoFactorUnavailable(u, err)
	}

	step, ok, err = totp.Validate(string(secret), code, time.Now())
	return step, ok, secret, err
}

// checkTOTP checks the code like verifyTOTP. If it is correct and the secret is encrypted with
// an old key, it encrypts it again with the current one (gradual rotation: each user is updated
// when they use two-factor authentication).
func (s *Service) checkTOTP(ctx context.Context, u store.User, code string) (int64, bool, error) {
	step, ok, secret, err := s.verifyTOTP(u, code)
	if err != nil || !ok {
		return step, ok, err
	}

	if box := s.opts.Box; box.NeedsRotation(*u.TOTPSecret) {
		s.reencryptSecret(ctx, box, u, secret)
	}
	return step, true, nil
}

// twoFactorUnavailable is the answer when the secret cannot be decrypted: its key is missing
// (it was removed from ENCRYPTION_PREVIOUS_KEYS before the rotation finished) or the data is
// damaged. The user sees a clear message; the log records the cause and the key for the
// administrator.
func twoFactorUnavailable(u store.User, cause error) error {
	return &httpx.Error{
		Status: http.StatusServiceUnavailable,
		Code:   "two_factor_unavailable",
		Err: fmt.Errorf("decrypt the two-factor secret of user %d (key %q): %w",
			u.ID, secretbox.KeyIDOf(*u.TOTPSecret), cause),
	}
}

// reencryptSecret encrypts the secret again with the current key. If it fails, it is only
// logged: the login was already valid, and the secret is tried again on the next use or with
// rotate-keys.
func (s *Service) reencryptSecret(ctx context.Context, box *secretbox.Box, u store.User, secret []byte) {
	encrypted, err := box.Encrypt(secret, totpAAD(u.ID))
	if err == nil {
		_, err = s.q.UpdateTOTPSecret(ctx, store.UpdateTOTPSecretParams{ID: u.ID, OldSecret: u.TOTPSecret, NewSecret: &encrypted})
	}
	if err != nil {
		httpx.LoggerFrom(ctx).Warn("could not encrypt the two-factor secret again", "user_id", u.ID, "error", err)
		return
	}
	httpx.LoggerFrom(ctx).Info("two-factor secret encrypted with the current key", "user_id", u.ID, "key_id", box.KeyID())
}

// rotateTwoFactorKeys is the two-factor part of RotateKeys: it encrypts again with the current
// key the secrets that use a previous one, and counts the recovery codes computed with previous
// keys (those cannot be computed again). It notes in inUse the keys that are still needed.
func (s *Service) rotateTwoFactorKeys(ctx context.Context, box *secretbox.Box, report *KeyReport, inUse map[string]bool) error {
	users, err := s.q.ListUsersWithTOTPSecret(ctx)
	if err != nil {
		return fmt.Errorf("list the two-factor secrets: %w", err)
	}

	for _, u := range users {
		old := *u.TOTPSecret
		if !box.NeedsRotation(old) {
			report.AlreadyCurrent++
			continue
		}

		secret, err := box.Decrypt(old, totpAAD(u.ID))
		if err != nil {
			reason := "damaged data or from a different key"
			if errors.Is(err, secretbox.ErrUnknownKey) {
				reason = "its key is not configured"
			}
			keyID := secretbox.KeyIDOf(old)
			report.Undecryptable = append(report.Undecryptable, UndecryptableSecret{UserID: u.ID, KeyID: keyID, Reason: reason})
			inUse[keyID] = true
			continue
		}

		encrypted, err := box.Encrypt(secret, totpAAD(u.ID))
		if err != nil {
			return err
		}
		rows, err := s.q.UpdateTOTPSecret(ctx, store.UpdateTOTPSecretParams{ID: u.ID, OldSecret: &old, NewSecret: &encrypted})
		if err != nil {
			return fmt.Errorf("store the secret of user %d: %w", u.ID, err)
		}
		if rows == 1 {
			report.Reencrypted++
		}
	}

	codes, err := s.q.CountRecoveryCodeUsersByKey(ctx)
	if err != nil {
		return fmt.Errorf("count the recovery codes: %w", err)
	}
	for _, c := range codes {
		if c.KeyID != box.KeyID() {
			report.OldRecoveryCodes = append(report.OldRecoveryCodes, KeyUsage{KeyID: c.KeyID, Users: c.Users})
			inUse[c.KeyID] = true
		}
	}
	return nil
}
