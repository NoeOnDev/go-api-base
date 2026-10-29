package testutil

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/NoeOnDev/go-api-base/internal/totp"
)

// TwoFactor is the enabled two-factor authentication of a test user.
type TwoFactor struct {
	Secret        string
	RecoveryCodes []string
}

// Code returns a valid authenticator app code. It uses the step after the current one: the
// current one was already used when enabling two-factor authentication, and the API rejects
// reused codes (the next step is accepted thanks to the ±1 step tolerance).
func (tf TwoFactor) Code(t *testing.T) string {
	t.Helper()
	return tf.CodeAt(t, time.Now().Add(totp.Period*time.Second))
}

// CodeAt returns the app code at a given time.
func (tf TwoFactor) CodeAt(t *testing.T, at time.Time) string {
	t.Helper()
	code, err := totp.Code(tf.Secret, at)
	if err != nil {
		t.Fatalf("compute the TOTP code: %v", err)
	}
	return code
}

// EnableTwoFactor turns on two-factor authentication for the user of the session, as the
// frontend would: it asks for the secret (setup) and confirms it with a code and the password
// (enable). For a user created with Login, the password is Password.
func EnableTwoFactor(t *testing.T, api http.Handler, cookie *http.Cookie, password string) TwoFactor {
	t.Helper()

	setup := Decode[struct {
		Secret string `json:"secret"`
	}](t, Do(t, api, cookie, http.MethodPost, "/auth/two-factor/setup", ""), http.StatusOK)

	code, err := totp.Code(setup.Secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	enabled := Decode[struct {
		RecoveryCodes []string `json:"recovery_codes"`
	}](t, Do(t, api, cookie, http.MethodPost, "/auth/two-factor/enable", fmt.Sprintf(`{"code":%q,"password":%q}`, code, password)), http.StatusOK)

	return TwoFactor{Secret: setup.Secret, RecoveryCodes: enabled.RecoveryCodes}
}
