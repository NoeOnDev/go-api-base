package auth_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/NoeOnDev/go-api-base/internal/auth"
	"github.com/NoeOnDev/go-api-base/internal/config"
	"github.com/NoeOnDev/go-api-base/internal/mail"
	"github.com/NoeOnDev/go-api-base/internal/testutil"
)

// Ojo: el login, enable y el desafío comparten el límite de 5 intentos por IP,
// así que cada test hace como mucho 5 peticiones a esas rutas.

// loginWithTwoFactor hace el primer paso del login y devuelve la cookie del desafío.
func loginWithTwoFactor(t *testing.T, api http.Handler) *http.Cookie {
	t.Helper()
	w := testutil.Do(t, api, nil, http.MethodPost, "/auth/login", loginBody)
	resp := testutil.Decode[map[string]bool](t, w, http.StatusAccepted)
	if !resp["two_factor_required"] {
		t.Fatalf("se esperaba two_factor_required: %v", resp)
	}
	var challenge *http.Cookie
	for _, c := range w.Result().Cookies() {
		switch c.Name {
		case auth.CookieName:
			t.Fatal("no debería crear la sesión antes del segundo paso")
		case auth.ChallengeCookieName:
			challenge = c
		}
	}
	if challenge == nil || !challenge.HttpOnly {
		t.Fatalf("falta la cookie HttpOnly del desafío: %+v", challenge)
	}
	return challenge
}

func challenge(t *testing.T, api http.Handler, cookie *http.Cookie, body string) *httptest.ResponseRecorder {
	t.Helper()
	return testutil.Do(t, api, cookie, http.MethodPost, "/auth/two-factor/challenge", body)
}

func TestLoginRequiresSecondFactor(t *testing.T) {
	pool := testutil.NewDB(t)
	api := testutil.NewAPI(pool)
	_, session := testutil.Login(t, pool, "ana@ejemplo.com", auth.RoleUser)
	tf := testutil.EnableTwoFactor(t, api, session, testutil.Password)

	pending := loginWithTwoFactor(t, api)

	// Un código incorrecto deja reintentar (hasta agotar los intentos del desafío).
	resp := testutil.Decode[testutil.APIError](t, challenge(t, api, pending, codeBody("000000")), http.StatusUnprocessableEntity)
	if resp.Error.Fields["code"] == "" {
		t.Errorf("se esperaba error en code: %+v", resp.Error)
	}

	w := challenge(t, api, pending, codeBody(tf.Code(t)))
	u := testutil.Decode[auth.User](t, w, http.StatusOK)
	if u.Email != "ana@ejemplo.com" || !u.TwoFactorEnabled {
		t.Errorf("usuario inesperado: %+v", u)
	}

	// Ahora sí hay sesión, y la cookie del desafío se borra.
	var newSession *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == auth.CookieName {
			newSession = c
		}
		if c.Name == auth.ChallengeCookieName && c.MaxAge >= 0 {
			t.Error("la cookie del desafío no se borró")
		}
	}
	if newSession == nil {
		t.Fatal("no se creó la sesión")
	}
	if w := testutil.Do(t, api, newSession, http.MethodGet, "/auth/me", ""); w.Code != http.StatusOK {
		t.Errorf("la sesión nueva no funciona: status = %d", w.Code)
	}
}

func TestTOTPCodeCannotBeReused(t *testing.T) {
	pool := testutil.NewDB(t)
	api := testutil.NewAPI(pool)
	_, session := testutil.Login(t, pool, "ana@ejemplo.com", auth.RoleUser)
	tf := testutil.EnableTwoFactor(t, api, session, testutil.Password)
	code := tf.Code(t)

	if w := challenge(t, api, loginWithTwoFactor(t, api), codeBody(code)); w.Code != http.StatusOK {
		t.Fatalf("primer uso: status = %d", w.Code)
	}

	// El mismo código, todavía dentro de su ventana de tiempo, ya no sirve.
	testutil.Decode[testutil.APIError](t, challenge(t, api, loginWithTwoFactor(t, api), codeBody(code)), http.StatusUnprocessableEntity)
}

func TestRecoveryCodeLogin(t *testing.T) {
	pool := testutil.NewDB(t)
	api := testutil.NewAPI(pool)
	_, session := testutil.Login(t, pool, "ana@ejemplo.com", auth.RoleUser)
	tf := testutil.EnableTwoFactor(t, api, session, testutil.Password)

	// Se acepta como lo escriba el usuario: en mayúsculas y sin guion.
	typed := strings.ToUpper(strings.ReplaceAll(tf.RecoveryCodes[0], "-", ""))
	body := fmt.Sprintf(`{"recovery_code":%q}`, typed)

	if w := challenge(t, api, loginWithTwoFactor(t, api), body); w.Code != http.StatusOK {
		t.Fatalf("código de recuperación: status = %d; cuerpo: %s", w.Code, w.Body)
	}

	// Cada código sirve una sola vez.
	resp := testutil.Decode[testutil.APIError](t, challenge(t, api, loginWithTwoFactor(t, api), body), http.StatusUnprocessableEntity)
	if resp.Error.Fields["recovery_code"] == "" {
		t.Errorf("se esperaba error en recovery_code: %+v", resp.Error)
	}
}

func TestTwoFactorChallengeErrors(t *testing.T) {
	pool := testutil.NewDB(t)
	api := testutil.NewAPI(pool)
	_, session := testutil.Login(t, pool, "ana@ejemplo.com", auth.RoleUser)
	tf := testutil.EnableTwoFactor(t, api, session, testutil.Password)

	// Sin la cookie del desafío (no pasó por el login).
	testutil.Decode[testutil.APIError](t, challenge(t, api, nil, codeBody(tf.Code(t))), http.StatusUnauthorized)

	pending := loginWithTwoFactor(t, api)

	// Hay que enviar uno de los dos códigos, no ambos.
	both := fmt.Sprintf(`{"code":%q,"recovery_code":%q}`, tf.Code(t), tf.RecoveryCodes[0])
	testutil.Decode[testutil.APIError](t, challenge(t, api, pending, both), http.StatusUnprocessableEntity)

	// Desafío vencido.
	if _, err := pool.Exec(t.Context(), "UPDATE user_tokens SET expires_at = now() - interval '1 minute'"); err != nil {
		t.Fatal(err)
	}
	testutil.Decode[testutil.APIError](t, challenge(t, api, pending, codeBody(tf.Code(t))), http.StatusUnauthorized)
}

func TestPasswordResetDoesNotBypassTwoFactor(t *testing.T) {
	pool := testutil.NewDB(t)
	mails := &mail.Recorder{}
	api := testutil.NewAPI(pool, testutil.WithMailer(mails))
	_, session := testutil.Login(t, pool, "ana@ejemplo.com", auth.RoleUser)
	testutil.EnableTwoFactor(t, api, session, testutil.Password)

	testutil.Do(t, api, nil, http.MethodPost, "/auth/password/forgot", `{"email":"ana@ejemplo.com"}`)
	msg, _ := mails.Last("ana@ejemplo.com")
	reset := fmt.Sprintf(`{"token":%q,"password":"contraseña-de-prueba"}`, testutil.TokenFromEmail(t, msg))
	if w := testutil.Do(t, api, nil, http.MethodPost, "/auth/password/reset", reset); w.Code != http.StatusNoContent {
		t.Fatalf("reset: status = %d", w.Code)
	}

	// Después de recuperar la contraseña, el login sigue pidiendo el segundo factor.
	loginWithTwoFactor(t, api)
}

// El segundo paso del 2FA tiene su propio límite: no gasta los intentos del login. Con el límite
// compartido de antes (5 por IP), este test se cortaba en el tercer login.
func TestTwoFactorChallengeHasItsOwnLimit(t *testing.T) {
	pool := testutil.NewDB(t)
	api := testutil.NewAPI(pool)
	_, session := testutil.Login(t, pool, "ana@ejemplo.com", auth.RoleUser)
	testutil.EnableTwoFactor(t, api, session, testutil.Password)

	// 10 logins con su segundo paso (con un código incorrecto), todos desde la misma IP.
	for i := range 10 {
		pending := loginWithTwoFactor(t, api) // falla el test si el login no responde 202
		if w := challenge(t, api, pending, codeBody("000000")); w.Code != http.StatusUnprocessableEntity {
			t.Fatalf("intento %d del 2FA: status = %d, se esperaba 422", i+1, w.Code)
		}
	}
}

// En producción (COOKIE_SECURE=true) la cookie del desafío también lleva el prefijo __Host-.
func TestChallengeCookieUsesHostPrefix(t *testing.T) {
	pool := testutil.NewDB(t)
	secure := testutil.WithConfig(func(c *config.Config) { c.CookieSecure = true })
	api := testutil.NewAPI(pool, secure)
	_, session := testutil.Login(t, pool, "ana@ejemplo.com", auth.RoleUser, secure)
	tf := testutil.EnableTwoFactor(t, api, session, testutil.Password)

	w := testutil.Do(t, api, nil, http.MethodPost, "/auth/login", loginBody)
	if w.Code != http.StatusAccepted {
		t.Fatalf("login: status = %d", w.Code)
	}
	pending := cookieNamed(t, w, "__Host-two_factor_challenge")
	if !pending.Secure || !pending.HttpOnly || pending.Path != "/" || pending.Domain != "" {
		t.Errorf("la cookie del desafío no cumple las reglas de __Host-: %+v", pending)
	}
	w = challenge(t, api, pending, codeBody(tf.Code(t)))
	if w.Code != http.StatusOK {
		t.Fatalf("desafío: status = %d; cuerpo: %s", w.Code, w.Body)
	}
	cookieNamed(t, w, "__Host-session")
}
