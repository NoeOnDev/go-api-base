package auth_test

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/NoeOnDev/go-api-base/internal/auth"
	"github.com/NoeOnDev/go-api-base/internal/store"
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

// EnableTOTP solo activa el secreto que se verificó: si mientras tanto otra petición pidió un QR
// nuevo (setup), no activa ese, que el usuario nunca escaneó y cuyo secreto recibió quien lo pidió.
func TestEnableTOTPRequiresVerifiedSecret(t *testing.T) {
	pool := testutil.NewDB(t)
	api := testutil.NewAPI(pool)
	user, cookie := testutil.Login(t, pool, "ana@ejemplo.com", auth.RoleUser)
	testutil.Decode[setupResponse](t, testutil.Do(t, api, cookie, http.MethodPost, "/auth/two-factor/setup", ""), http.StatusOK)

	q := store.New(pool)
	verified, err := q.GetUserByID(t.Context(), user.ID) // el secreto con el que enable verificó el código
	if err != nil {
		t.Fatal(err)
	}
	// Antes de activarlo, otra petición pide un QR nuevo.
	testutil.Decode[setupResponse](t, testutil.Do(t, api, cookie, http.MethodPost, "/auth/two-factor/setup", ""), http.StatusOK)

	step := int64(1)
	rows, err := q.EnableTOTP(t.Context(), store.EnableTOTPParams{ID: user.ID, TOTPSecret: verified.TOTPSecret, TOTPLastStep: &step})
	if err != nil || rows != 0 {
		t.Fatalf("EnableTOTP con un secreto reemplazado: filas = %d, error = %v; se esperaba 0", rows, err)
	}
	if me := testutil.Decode[auth.User](t, testutil.Do(t, api, cookie, http.MethodGet, "/auth/me", ""), http.StatusOK); me.TwoFactorEnabled {
		t.Error("el 2FA no debería haber quedado activo")
	}
}

// Si un paso de activar el 2FA falla, sigue inactivo: sin códigos de recuperación y con las demás
// sesiones abiertas. El mismo código sirve al reintentar, porque su paso no quedó guardado.
func TestEnableTwoFactorIsAtomic(t *testing.T) {
	pool := testutil.NewDB(t)
	api := testutil.NewAPI(pool)
	u, cookie := testutil.Login(t, pool, "ana@ejemplo.com", auth.RoleUser)
	other := newSession(t, pool, u.ID) // otra sesión, que se cerraría

	setup := testutil.Decode[struct {
		Secret string `json:"secret"`
	}](t, testutil.Do(t, api, cookie, http.MethodPost, "/auth/two-factor/setup", ""), http.StatusOK)
	code, err := totp.Code(setup.Secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}

	exec := func(sql string) error { _, err := pool.Exec(context.Background(), sql); return err }
	failSessionDeletes(t, exec)

	// Falla el cierre de las demás sesiones, el último paso: se deshace todo.
	testutil.Decode[testutil.APIError](t, testutil.Do(t, api, cookie, http.MethodPost, "/auth/two-factor/enable", enableBody(code, testutil.Password)), http.StatusInternalServerError)

	if me := testutil.Decode[auth.User](t, testutil.Do(t, api, cookie, http.MethodGet, "/auth/me", ""), http.StatusOK); me.TwoFactorEnabled {
		t.Error("el 2FA no debería haber quedado activo")
	}
	if n := countRows(t, pool, "user_recovery_codes", u.ID); n != 0 {
		t.Errorf("quedaron %d códigos de recuperación, se esperaba ninguno", n)
	}
	if w := testutil.Do(t, api, other, http.MethodGet, "/auth/me", ""); w.Code != http.StatusOK {
		t.Errorf("la otra sesión debería seguir abierta: status = %d", w.Code)
	}

	// Sin la falla, el mismo código funciona.
	if err := exec("DROP TRIGGER test_fail_session_delete ON sessions"); err != nil {
		t.Fatal(err)
	}
	if w := testutil.Do(t, api, cookie, http.MethodPost, "/auth/two-factor/enable", enableBody(code, testutil.Password)); w.Code != http.StatusOK {
		t.Fatalf("el reintento debería activar el 2FA: status = %d; cuerpo: %s", w.Code, w.Body)
	}
}

// Lo mismo al desactivarlo: sigue activo, con sus códigos y las demás sesiones.
func TestDisableTwoFactorIsAtomic(t *testing.T) {
	pool := testutil.NewDB(t)
	api := testutil.NewAPI(pool)
	u, cookie := testutil.Login(t, pool, "ana@ejemplo.com", auth.RoleUser)
	testutil.EnableTwoFactor(t, api, cookie, testutil.Password)
	other := newSession(t, pool, u.ID) // después de activarlo, que cierra las demás

	exec := func(sql string) error { _, err := pool.Exec(context.Background(), sql); return err }
	failSessionDeletes(t, exec)

	body := fmt.Sprintf(`{"password":%q}`, testutil.Password)
	testutil.Decode[testutil.APIError](t, testutil.Do(t, api, cookie, http.MethodPost, "/auth/two-factor/disable", body), http.StatusInternalServerError)

	if me := testutil.Decode[auth.User](t, testutil.Do(t, api, cookie, http.MethodGet, "/auth/me", ""), http.StatusOK); !me.TwoFactorEnabled {
		t.Error("el 2FA debería seguir activo")
	}
	if n := countRows(t, pool, "user_recovery_codes", u.ID); n == 0 {
		t.Error("se borraron los códigos de recuperación")
	}
	if w := testutil.Do(t, api, other, http.MethodGet, "/auth/me", ""); w.Code != http.StatusOK {
		t.Errorf("la otra sesión debería seguir abierta: status = %d", w.Code)
	}
}
