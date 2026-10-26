package auth_test

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/NoeOnDev/go-api-base/internal/auth"
	"github.com/NoeOnDev/go-api-base/internal/testutil"
	"github.com/NoeOnDev/go-api-base/internal/totp"
)

type setupResponse struct {
	Secret     string `json:"secret"`
	OTPAuthURL string `json:"otpauth_url"`
}

type recoveryCodes struct {
	RecoveryCodes []string `json:"recovery_codes"`
}

func codeBody(code string) string { return fmt.Sprintf(`{"code":%q}`, code) }

func enableBody(code, password string) string {
	return fmt.Sprintf(`{"code":%q,"password":%q}`, code, password)
}

func TestEnableTwoFactor(t *testing.T) {
	pool := testutil.NewDB(t)
	api := testutil.NewAPI(pool)
	user, cookie := testutil.Login(t, pool, "ana@ejemplo.com", auth.RoleUser)
	other := newSession(t, pool, user.ID)

	// Setup devuelve el secreto y la URL del QR, con el nombre de la app y el email.
	setup := testutil.Decode[setupResponse](t, testutil.Do(t, api, cookie, http.MethodPost, "/auth/two-factor/setup", ""), http.StatusOK)
	u, err := url.Parse(setup.OTPAuthURL)
	if err != nil || u.Scheme != "otpauth" || u.Query().Get("secret") != setup.Secret || u.Query().Get("issuer") != "App de Prueba" ||
		!strings.Contains(u.Path, "ana@ejemplo.com") {
		t.Fatalf("otpauth_url inesperada: %s", setup.OTPAuthURL)
	}

	// En la base el secreto queda cifrado, no en base32.
	var stored string
	if err := pool.QueryRow(t.Context(), "SELECT totp_secret FROM users WHERE id = $1", user.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored == setup.Secret || strings.Contains(stored, setup.Secret) {
		t.Error("el secreto está guardado sin cifrar")
	}

	// Mientras no se confirma, el 2FA no está activo.
	me := testutil.Decode[auth.User](t, testutil.Do(t, api, cookie, http.MethodGet, "/auth/me", ""), http.StatusOK)
	if me.TwoFactorEnabled {
		t.Fatal("el 2FA no debería estar activo antes de confirmarlo")
	}

	// Pide la contraseña: con una sesión robada, alguien activaría el 2FA con su propia app y,
	// al cerrarse las demás sesiones, dejaría al dueño afuera. Ojo: la ruta tiene límite por
	// usuario (5 seguidos); este test hace 4 peticiones a ella.
	code, _ := totp.Code(setup.Secret, time.Now())
	for name, tt := range map[string]struct{ body, field string }{
		"sin contraseña":        {codeBody(code), "password"},
		"contraseña incorrecta": {enableBody(code, "incorrecta"), "password"},
		"código incorrecto":     {enableBody("000000", "contraseña-de-prueba"), "code"},
	} {
		resp := testutil.Decode[testutil.APIError](t,
			testutil.Do(t, api, cookie, http.MethodPost, "/auth/two-factor/enable", tt.body), http.StatusUnprocessableEntity)
		if resp.Error.Fields[tt.field] == "" {
			t.Errorf("%s: se esperaba error en %s: %+v", name, tt.field, resp.Error)
		}
	}
	me = testutil.Decode[auth.User](t, testutil.Do(t, api, cookie, http.MethodGet, "/auth/me", ""), http.StatusOK)
	if me.TwoFactorEnabled {
		t.Fatal("el 2FA no debería activarse sin la contraseña y el código correctos")
	}

	codes := testutil.Decode[recoveryCodes](t,
		testutil.Do(t, api, cookie, http.MethodPost, "/auth/two-factor/enable", enableBody(code, "contraseña-de-prueba")), http.StatusOK)

	if len(codes.RecoveryCodes) != 10 {
		t.Fatalf("se esperaban 10 códigos de recuperación: %v", codes.RecoveryCodes)
	}
	for _, c := range codes.RecoveryCodes {
		if len(c) != 11 || c[5] != '-' {
			t.Errorf("código con formato inesperado: %q", c)
		}
	}

	// Los códigos no se guardan en texto plano.
	var plain int
	if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM user_recovery_codes WHERE code_hash = convert_to($1, 'UTF8')",
		codes.RecoveryCodes[0]).Scan(&plain); err != nil || plain != 0 {
		t.Errorf("código de recuperación en texto plano: %d (%v)", plain, err)
	}

	me = testutil.Decode[auth.User](t, testutil.Do(t, api, cookie, http.MethodGet, "/auth/me", ""), http.StatusOK)
	if !me.TwoFactorEnabled {
		t.Error("el 2FA debería estar activo")
	}

	// Activar el 2FA cierra las demás sesiones.
	if w := testutil.Do(t, api, other, http.MethodGet, "/auth/me", ""); w.Code != http.StatusUnauthorized {
		t.Errorf("la otra sesión sigue activa: status = %d", w.Code)
	}

	// Ya activo, setup y enable responden 409.
	testutil.Decode[testutil.APIError](t, testutil.Do(t, api, cookie, http.MethodPost, "/auth/two-factor/setup", ""), http.StatusConflict)
}

func TestEnableTwoFactorRequiresSetup(t *testing.T) {
	pool := testutil.NewDB(t)
	api := testutil.NewAPI(pool)
	_, cookie := testutil.Login(t, pool, "ana@ejemplo.com", auth.RoleUser)

	testutil.Decode[testutil.APIError](t,
		testutil.Do(t, api, cookie, http.MethodPost, "/auth/two-factor/enable", enableBody("123456", "contraseña-de-prueba")), http.StatusConflict)
	testutil.Decode[testutil.APIError](t,
		testutil.Do(t, api, nil, http.MethodPost, "/auth/two-factor/setup", ""), http.StatusUnauthorized)
}

func TestDisableTwoFactor(t *testing.T) {
	pool := testutil.NewDB(t)
	api := testutil.NewAPI(pool)
	_, cookie := testutil.Login(t, pool, "ana@ejemplo.com", auth.RoleUser)
	testutil.EnableTwoFactor(t, api, cookie, testutil.Password)

	resp := testutil.Decode[testutil.APIError](t,
		testutil.Do(t, api, cookie, http.MethodPost, "/auth/two-factor/disable", `{"password":"incorrecta"}`), http.StatusUnprocessableEntity)
	if resp.Error.Fields["password"] == "" {
		t.Errorf("se esperaba error en password: %+v", resp.Error)
	}

	if w := testutil.Do(t, api, cookie, http.MethodPost, "/auth/two-factor/disable", `{"password":"contraseña-de-prueba"}`); w.Code != http.StatusNoContent {
		t.Fatalf("disable: status = %d; cuerpo: %s", w.Code, w.Body)
	}

	me := testutil.Decode[auth.User](t, testutil.Do(t, api, cookie, http.MethodGet, "/auth/me", ""), http.StatusOK)
	if me.TwoFactorEnabled {
		t.Error("el 2FA debería estar desactivado")
	}
	var codes int
	if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM user_recovery_codes").Scan(&codes); err != nil || codes != 0 {
		t.Errorf("quedaron %d códigos de recuperación (%v)", codes, err)
	}
}

func TestRegenerateRecoveryCodes(t *testing.T) {
	pool := testutil.NewDB(t)
	api := testutil.NewAPI(pool)
	_, cookie := testutil.Login(t, pool, "ana@ejemplo.com", auth.RoleUser)

	// Sin 2FA no hay códigos que regenerar.
	testutil.Decode[testutil.APIError](t,
		testutil.Do(t, api, cookie, http.MethodPost, "/auth/two-factor/recovery-codes", `{"password":"contraseña-de-prueba"}`), http.StatusConflict)

	first := testutil.EnableTwoFactor(t, api, cookie, testutil.Password)
	second := testutil.Decode[recoveryCodes](t,
		testutil.Do(t, api, cookie, http.MethodPost, "/auth/two-factor/recovery-codes", `{"password":"contraseña-de-prueba"}`), http.StatusOK)

	if len(second.RecoveryCodes) != 10 || second.RecoveryCodes[0] == first.RecoveryCodes[0] {
		t.Errorf("códigos nuevos inesperados: %v", second.RecoveryCodes)
	}
	var count int
	if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM user_recovery_codes").Scan(&count); err != nil || count != 10 {
		t.Errorf("deberían quedar solo los 10 códigos nuevos, hay %d (%v)", count, err)
	}
}
