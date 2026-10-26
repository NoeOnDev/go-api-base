package auth_test

import (
	"bytes"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/NoeOnDev/go-api-base/internal/auth"
	"github.com/NoeOnDev/go-api-base/internal/config"
	"github.com/NoeOnDev/go-api-base/internal/secretbox"
	"github.com/NoeOnDev/go-api-base/internal/testutil"
)

// Clave A: la de los tests (testutil.EncryptionKey). Clave B: la nueva, después de rotar.
var keyB = bytes.Repeat([]byte{2}, 32)

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
