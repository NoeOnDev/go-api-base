package auth_test

import (
	"bytes"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/NoeOnDev/go-api-base/internal/auth"
	"github.com/NoeOnDev/go-api-base/internal/config"
	"github.com/NoeOnDev/go-api-base/internal/secretbox"
	"github.com/NoeOnDev/go-api-base/internal/testutil"
	"github.com/NoeOnDev/go-api-base/internal/totp"
)

// Clave A: la de los tests (testutil.EncryptionKey). Clave B: la nueva, después de rotar.
var keyB = bytes.Repeat([]byte{2}, 32)

// serviceWithKeys crea el servicio de auth con un llavero, como lo hace el comando rotate-keys.
func serviceWithKeys(t *testing.T, pool *pgxpool.Pool, current []byte, previous ...[]byte) (*auth.Service, *secretbox.Box) {
	t.Helper()
	box, err := secretbox.New(current, previous...)
	if err != nil {
		t.Fatal(err)
	}
	return auth.NewService(pool, auth.Options{Box: box}), box
}

// rotatedAPI es la API después de rotar: B es la actual y A sigue como anterior.
func rotatedAPI(pool *pgxpool.Pool) http.Handler {
	return testutil.NewAPI(pool, testutil.WithConfig(func(c *config.Config) {
		c.EncryptionKey = keyB
		c.EncryptionPreviousKeys = [][]byte{testutil.EncryptionKey}
	}))
}

// onlyNewKeyAPI es la API si se quitó la clave A antes de terminar la rotación.
func onlyNewKeyAPI(pool *pgxpool.Pool) http.Handler {
	return testutil.NewAPI(pool, testutil.WithConfig(func(c *config.Config) {
		c.EncryptionKey = keyB
	}))
}

func storedSecret(t *testing.T, pool *pgxpool.Pool, email string) string {
	t.Helper()
	var s string
	if err := pool.QueryRow(t.Context(), "SELECT totp_secret FROM users WHERE email = $1", email).Scan(&s); err != nil {
		t.Fatal(err)
	}
	return s
}

func loginAs(t *testing.T, api http.Handler, email string) *http.Cookie {
	t.Helper()
	body := fmt.Sprintf(`{"email":%q,"password":"contraseña-de-prueba"}`, email)
	w := testutil.Do(t, api, nil, http.MethodPost, "/auth/login", body)
	testutil.Decode[map[string]bool](t, w, http.StatusAccepted)
	for _, c := range w.Result().Cookies() {
		if c.Name == auth.ChallengeCookieName {
			return c
		}
	}
	t.Fatal("falta la cookie del desafío")
	return nil
}

func TestTwoFactorAfterKeyRotation(t *testing.T) {
	pool := testutil.NewDB(t)
	_, session := testutil.Login(t, pool, "ana@ejemplo.com", auth.RoleUser)
	tf := testutil.EnableTwoFactor(t, testutil.NewAPI(pool), session, testutil.Password) // con la clave A

	boxB, _ := secretbox.New(keyB)
	before := storedSecret(t, pool, "ana@ejemplo.com")
	if strings.HasPrefix(before, boxB.KeyID()+":") {
		t.Fatal("el secreto debería estar cifrado con la clave A")
	}

	api := rotatedAPI(pool)

	// El login con código funciona con la clave anterior…
	if w := challenge(t, api, loginAs(t, api, "ana@ejemplo.com"), codeBody(tf.Code(t))); w.Code != http.StatusOK {
		t.Fatalf("login con la clave anterior: status = %d; cuerpo: %s", w.Code, w.Body)
	}
	// …y de paso el secreto quedó cifrado con la clave nueva.
	if after := storedSecret(t, pool, "ana@ejemplo.com"); !strings.HasPrefix(after, boxB.KeyID()+":") {
		t.Errorf("el secreto no se volvió a cifrar con la clave nueva: %s", after)
	}

	// Un código de recuperación generado con la clave A sigue sirviendo.
	body := fmt.Sprintf(`{"recovery_code":%q}`, tf.RecoveryCodes[0])
	if w := challenge(t, api, loginAs(t, api, "ana@ejemplo.com"), body); w.Code != http.StatusOK {
		t.Errorf("código de recuperación de la clave anterior: status = %d", w.Code)
	}
}

func TestTwoFactorWithMissingKey(t *testing.T) {
	pool := testutil.NewDB(t)
	_, session := testutil.Login(t, pool, "ana@ejemplo.com", auth.RoleUser)
	tf := testutil.EnableTwoFactor(t, testutil.NewAPI(pool), session, testutil.Password) // con la clave A

	// Se quitó la clave A sin rotar: el usuario recibe un error claro, no un 500.
	api := onlyNewKeyAPI(pool)
	resp := testutil.Decode[testutil.APIError](t,
		challenge(t, api, loginAs(t, api, "ana@ejemplo.com"), codeBody(tf.Code(t))), http.StatusServiceUnavailable)
	if resp.Error.Code != "two_factor_unavailable" || strings.Contains(resp.Error.Message, "clave") {
		t.Errorf("error inesperado (no debe exponer detalles internos): %+v", resp.Error)
	}

	// Los códigos de recuperación de la clave A tampoco sirven sin ella.
	body := fmt.Sprintf(`{"recovery_code":%q}`, tf.RecoveryCodes[0])
	testutil.Decode[testutil.APIError](t, challenge(t, api, loginAs(t, api, "ana@ejemplo.com"), body), http.StatusUnprocessableEntity)
}

func TestTwoFactorDataIsBoundToUser(t *testing.T) {
	pool := testutil.NewDB(t)
	setup := testutil.NewAPI(pool)
	_, ana := testutil.Login(t, pool, "ana@ejemplo.com", auth.RoleUser)
	luis, luisSession := testutil.Login(t, pool, "luis@ejemplo.com", auth.RoleUser)
	tfAna := testutil.EnableTwoFactor(t, setup, ana, testutil.Password)
	testutil.EnableTwoFactor(t, setup, luisSession, testutil.Password)

	// Alguien con acceso de escritura a la base copia el secreto de Ana a Luis
	// y le pasa los códigos de recuperación de Ana.
	for _, q := range []string{
		"UPDATE users SET totp_secret = (SELECT totp_secret FROM users WHERE email = 'ana@ejemplo.com') WHERE email = 'luis@ejemplo.com'",
		"DELETE FROM user_recovery_codes WHERE user_id = $1",
		"UPDATE user_recovery_codes SET user_id = $1",
	} {
		args := []any{luis.ID}
		if !strings.Contains(q, "$1") {
			args = nil
		}
		if _, err := pool.Exec(t.Context(), q, args...); err != nil {
			t.Fatal(err)
		}
	}

	// Con la app de Ana no puede entrar como Luis: el secreto no se descifra fuera de su usuario.
	api := testutil.NewAPI(pool)
	resp := testutil.Decode[testutil.APIError](t,
		challenge(t, api, loginAs(t, api, "luis@ejemplo.com"), codeBody(tfAna.Code(t))), http.StatusServiceUnavailable)
	if resp.Error.Code != "two_factor_unavailable" {
		t.Errorf("error inesperado: %+v", resp.Error)
	}

	// Los códigos de recuperación de Ana tampoco le sirven a Luis.
	body := fmt.Sprintf(`{"recovery_code":%q}`, tfAna.RecoveryCodes[0])
	testutil.Decode[testutil.APIError](t, challenge(t, api, loginAs(t, api, "luis@ejemplo.com"), body), http.StatusUnprocessableEntity)
}

// Un secreto pendiente cifrado con una clave anterior se activa igual. Enable no lo vuelve a
// cifrar (lo hacen el login y rotate-keys): si lo hiciera, el secreto ya no coincidiría con el
// que se verificó y EnableTOTP no lo activaría.
func TestEnableTwoFactorAfterKeyRotation(t *testing.T) {
	pool := testutil.NewDB(t)
	_, session := testutil.Login(t, pool, "ana@ejemplo.com", auth.RoleUser)
	setup := testutil.Decode[setupResponse](t, testutil.Do(t, testutil.NewAPI(pool), session, http.MethodPost, "/auth/two-factor/setup", ""), http.StatusOK) // con la clave A

	api := rotatedAPI(pool)
	code, err := totp.Code(setup.Secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if w := testutil.Do(t, api, session, http.MethodPost, "/auth/two-factor/enable", enableBody(code, testutil.Password)); w.Code != http.StatusOK {
		t.Fatalf("activar con un secreto de la clave anterior: status = %d; cuerpo: %s", w.Code, w.Body)
	}

	// El login con código funciona, y ahí el secreto pasa a la clave nueva.
	tf := testutil.TwoFactor{Secret: setup.Secret}
	if w := challenge(t, api, loginAs(t, api, "ana@ejemplo.com"), codeBody(tf.Code(t))); w.Code != http.StatusOK {
		t.Fatalf("login después de activar: status = %d; cuerpo: %s", w.Code, w.Body)
	}
	boxB, _ := secretbox.New(keyB)
	if after := storedSecret(t, pool, "ana@ejemplo.com"); !strings.HasPrefix(after, boxB.KeyID()+":") {
		t.Errorf("el secreto no se volvió a cifrar con la clave nueva en el login: %s", after)
	}
}

func TestRotateKeys(t *testing.T) {
	pool := testutil.NewDB(t)
	apiA := testutil.NewAPI(pool) // clave A

	// Dos usuarios con 2FA activo y uno con el 2FA a medio configurar, todos con la clave A.
	_, ana := testutil.Login(t, pool, "ana@ejemplo.com", auth.RoleUser)
	_, luis := testutil.Login(t, pool, "luis@ejemplo.com", auth.RoleUser)
	_, sofia := testutil.Login(t, pool, "sofia@ejemplo.com", auth.RoleUser)
	testutil.EnableTwoFactor(t, apiA, ana, testutil.Password)
	testutil.EnableTwoFactor(t, apiA, luis, testutil.Password)
	testutil.Decode[setupResponse](t, testutil.Do(t, apiA, sofia, http.MethodPost, "/auth/two-factor/setup", ""), http.StatusOK)

	// Rotación: B es la actual y A queda como anterior.
	service, boxB := serviceWithKeys(t, pool, keyB, testutil.EncryptionKey)
	boxA, _ := secretbox.New(testutil.EncryptionKey)

	report, err := service.RotateKeys(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if report.CurrentKeyID != boxB.KeyID() || report.Reencrypted != 3 || report.AlreadyCurrent != 0 {
		t.Errorf("reporte inesperado: %+v", report)
	}
	// Los códigos de recuperación de A no se pueden recalcular: el reporte lo avisa.
	if len(report.OldRecoveryCodes) != 1 || report.OldRecoveryCodes[0] != (auth.KeyUsage{KeyID: boxA.KeyID(), Users: 2}) {
		t.Errorf("códigos viejos inesperados: %+v", report.OldRecoveryCodes)
	}
	if report.Done() || len(report.RemovableKeys) != 0 {
		t.Errorf("la clave A todavía se usa y no debería poder quitarse: %+v", report)
	}

	// Una segunda pasada no vuelve a cifrar nada.
	again, err := service.RotateKeys(t.Context())
	if err != nil || again.Reencrypted != 0 || again.AlreadyCurrent != 3 {
		t.Errorf("segunda pasada inesperada: %+v (%v)", again, err)
	}

	// Cuando los dos usuarios regeneran sus códigos con la clave nueva, la rotación termina.
	apiB := testutil.NewAPI(pool, testutil.WithConfig(func(c *config.Config) {
		c.EncryptionKey = keyB
		c.EncryptionPreviousKeys = [][]byte{testutil.EncryptionKey}
	}))
	for _, cookie := range []*http.Cookie{ana, luis} {
		testutil.Decode[recoveryCodes](t,
			testutil.Do(t, apiB, cookie, http.MethodPost, "/auth/two-factor/recovery-codes", `{"password":"contraseña-de-prueba"}`), http.StatusOK)
	}

	done, err := service.RotateKeys(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !done.Done() || len(done.RemovableKeys) != 1 || done.RemovableKeys[0] != boxA.KeyID() {
		t.Errorf("la rotación debería estar completa y la clave A se puede quitar: %+v", done)
	}
}

func TestRotateKeysReportsUndecryptable(t *testing.T) {
	pool := testutil.NewDB(t)

	// Un usuario con el secreto cifrado con una clave C que no está en el llavero.
	keyC := bytes.Repeat([]byte{3}, 32)
	apiC := testutil.NewAPI(pool, testutil.WithConfig(func(c *config.Config) { c.EncryptionKey = keyC }))
	user, cookie := testutil.Login(t, pool, "ana@ejemplo.com", auth.RoleUser)
	testutil.EnableTwoFactor(t, apiC, cookie, testutil.Password)

	service, _ := serviceWithKeys(t, pool, keyB, testutil.EncryptionKey)
	report, err := service.RotateKeys(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	boxC, _ := secretbox.New(keyC)
	if len(report.Undecryptable) != 1 || report.Undecryptable[0].UserID != user.ID || report.Undecryptable[0].KeyID != boxC.KeyID() {
		t.Errorf("se esperaba un secreto sin descifrar del usuario %d: %+v", user.ID, report.Undecryptable)
	}
	if report.Done() {
		t.Error("la rotación no debería darse por terminada")
	}
}
