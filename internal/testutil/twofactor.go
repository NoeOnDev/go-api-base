package testutil

import (
	"bytes"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/NoeOnDev/go-api-base/internal/totp"
)

// EncryptionKey es la ENCRYPTION_KEY de los tests: 32 bytes fijos, no es un secreto.
var EncryptionKey = bytes.Repeat([]byte{1}, 32)

// TwoFactor es el 2FA activado de un usuario de prueba.
type TwoFactor struct {
	Secret        string
	RecoveryCodes []string
}

// Code devuelve un código válido de la app de autenticación. Usa el paso siguiente al actual:
// el actual ya se usó al activar el 2FA, y la API rechaza los códigos reutilizados
// (el paso siguiente se acepta por la tolerancia de ±1 paso).
func (tf TwoFactor) Code(t *testing.T) string {
	t.Helper()
	return tf.CodeAt(t, time.Now().Add(totp.Period*time.Second))
}

// CodeAt devuelve el código de la app en un momento dado.
func (tf TwoFactor) CodeAt(t *testing.T, at time.Time) string {
	t.Helper()
	code, err := totp.Code(tf.Secret, at)
	if err != nil {
		t.Fatalf("calcular código TOTP: %v", err)
	}
	return code
}

// EnableTwoFactor activa el 2FA del usuario de la sesión, como lo haría el frontend:
// pide el secreto (setup) y lo confirma con un código y la contraseña (enable).
// El usuario tiene que ser uno creado con Login; si no, usar EnableTwoFactorWithPassword.
func EnableTwoFactor(t *testing.T, api http.Handler, cookie *http.Cookie) TwoFactor {
	t.Helper()
	return EnableTwoFactorWithPassword(t, api, cookie, testPassword)
}

// EnableTwoFactorWithPassword es EnableTwoFactor para un usuario con otra contraseña
// (por ejemplo, uno creado con POST /auth/register).
func EnableTwoFactorWithPassword(t *testing.T, api http.Handler, cookie *http.Cookie, password string) TwoFactor {
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
