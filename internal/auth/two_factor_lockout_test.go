package auth_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/NoeOnDev/go-api-base/internal/auth"
	"github.com/NoeOnDev/go-api-base/internal/mail"
	"github.com/NoeOnDev/go-api-base/internal/testutil"
)

// Estos tests simulan un atacante que tiene la contraseña y reparte sus intentos entre
// muchas IPs: cada petición sale de una IP distinta, así que el límite por IP no lo frena.

func loginFromIP(t *testing.T, api http.Handler, ip string) *http.Cookie {
	t.Helper()
	w := testutil.DoFromIP(t, api, ip, nil, http.MethodPost, "/auth/login", loginBody)
	testutil.Decode[map[string]bool](t, w, http.StatusAccepted)
	for _, c := range w.Result().Cookies() {
		if c.Name == auth.ChallengeCookieName {
			return c
		}
	}
	t.Fatal("falta la cookie del desafío")
	return nil
}

func challengeFromIP(t *testing.T, api http.Handler, ip string, cookie *http.Cookie, body string) *httptest.ResponseRecorder {
	t.Helper()
	return testutil.DoFromIP(t, api, ip, cookie, http.MethodPost, "/auth/two-factor/challenge", body)
}

const lockedSubject = "Intento de inicio de sesión bloqueado"

func TestChallengeLocksAfterMaxAttempts(t *testing.T) {
	pool := testutil.NewDB(t)
	mails := &mail.Recorder{}
	api := testutil.NewAPI(pool, testutil.WithMailer(mails))
	_, session := testutil.Login(t, pool, "ana@ejemplo.com", auth.RoleUser)
	tf := testutil.EnableTwoFactor(t, api, session, testutil.Password)
	addr := &ips{}

	pending := loginFromIP(t, api, addr.next())

	// Un cuerpo mal armado no gasta intentos.
	testutil.Decode[testutil.APIError](t, challengeFromIP(t, api, addr.next(), pending, `{}`), http.StatusUnprocessableEntity)

	// Los primeros 4 códigos incorrectos dejan reintentar (uno es de recuperación: cuentan igual).
	for i := range 4 {
		body := codeBody("000000")
		if i == 0 {
			body = `{"recovery_code":"aaaaa-bbbbb"}`
		}
		testutil.Decode[testutil.APIError](t, challengeFromIP(t, api, addr.next(), pending, body), http.StatusUnprocessableEntity)
	}

	// El quinto corta el desafío.
	lastIP := addr.next()
	w := challengeFromIP(t, api, lastIP, pending, codeBody("000000"))
	resp := testutil.Decode[testutil.APIError](t, w, http.StatusUnauthorized)
	if resp.Error.Code != "two_factor_locked" {
		t.Errorf("se esperaba two_factor_locked: %+v", resp.Error)
	}
	for _, c := range w.Result().Cookies() {
		if c.Name == auth.ChallengeCookieName && c.MaxAge >= 0 {
			t.Error("la cookie del desafío no se borró")
		}
	}

	// El usuario recibe el aviso, con la IP del último intento.
	msg, ok := mails.Last("ana@ejemplo.com")
	if !ok || msg.Subject != lockedSubject || !strings.Contains(msg.Body, lastIP) || !strings.Contains(msg.Body, "https://app.test") {
		t.Errorf("aviso inesperado (%v): %+v", ok, msg)
	}

	// Ese desafío ya no sirve, ni siquiera con el código correcto…
	resp = testutil.Decode[testutil.APIError](t, challengeFromIP(t, api, addr.next(), pending, codeBody(tf.Code(t))), http.StatusUnauthorized)
	if resp.Error.Code == "two_factor_locked" {
		t.Errorf("un desafío borrado debería responder como vencido: %+v", resp.Error)
	}

	// …pero volviendo a poner la contraseña, el código correcto funciona.
	if w := challengeFromIP(t, api, addr.next(), loginFromIP(t, api, addr.next()), codeBody(tf.Code(t))); w.Code != http.StatusOK {
		t.Errorf("login nuevo: status = %d; cuerpo: %s", w.Code, w.Body)
	}
}

func TestChallengeUserLimitAcrossIPs(t *testing.T) {
	pool := testutil.NewDB(t)
	mails := &mail.Recorder{}
	api := testutil.NewAPI(pool, testutil.WithMailer(mails))
	_, session := testutil.Login(t, pool, "ana@ejemplo.com", auth.RoleUser)
	tf := testutil.EnableTwoFactor(t, api, session, testutil.Password)
	addr := &ips{}

	// Dos desafíos agotados: 10 códigos incorrectos, cada uno desde una IP distinta.
	for range 2 {
		pending := loginFromIP(t, api, addr.next())
		for range 5 {
			challengeFromIP(t, api, addr.next(), pending, codeBody("000000"))
		}
	}

	// Con un desafío nuevo y otra IP, el siguiente intento se rechaza, aunque traiga el código correcto.
	w := challengeFromIP(t, api, addr.next(), loginFromIP(t, api, addr.next()), codeBody(tf.Code(t)))
	resp := testutil.Decode[testutil.APIError](t, w, http.StatusTooManyRequests)
	if resp.Error.Code != "too_many_requests" || w.Header().Get("Retry-After") != "360" {
		t.Errorf("respuesta inesperada: %+v, Retry-After %q", resp.Error, w.Header().Get("Retry-After"))
	}

	// Un aviso por cada desafío agotado.
	sent := 0
	for _, m := range mails.Messages() {
		if m.Subject == lockedSubject {
			sent++
		}
	}
	if sent != 2 {
		t.Errorf("se enviaron %d avisos, se esperaban 2", sent)
	}
}

func TestPasswordResetUnlocksTwoFactor(t *testing.T) {
	pool := testutil.NewDB(t)
	mails := &mail.Recorder{}
	api := testutil.NewAPI(pool, testutil.WithMailer(mails))
	_, session := testutil.Login(t, pool, "ana@ejemplo.com", auth.RoleUser)
	tf := testutil.EnableTwoFactor(t, api, session, testutil.Password)
	addr := &ips{}

	// Alguien con la contraseña agota el límite del 2FA de Ana y deja un desafío pendiente.
	for range 2 {
		pending := loginFromIP(t, api, addr.next())
		for range 5 {
			challengeFromIP(t, api, addr.next(), pending, codeBody("000000"))
		}
	}
	attacker := loginFromIP(t, api, addr.next())

	// Ana recupera su contraseña.
	testutil.DoFromIP(t, api, addr.next(), nil, http.MethodPost, "/auth/password/forgot", `{"email":"ana@ejemplo.com"}`)
	msg, _ := mails.Last("ana@ejemplo.com")
	if w := testutil.DoFromIP(t, api, addr.next(), nil, http.MethodPost, "/auth/password/reset", resetBody(testutil.TokenFromEmail(t, msg), newPassword)); w.Code != http.StatusNoContent {
		t.Fatalf("reset: status = %d", w.Code)
	}

	// El desafío que se obtuvo con la contraseña vieja ya no sirve…
	testutil.Decode[testutil.APIError](t, challengeFromIP(t, api, addr.next(), attacker, codeBody(tf.Code(t))), http.StatusUnauthorized)

	// …y Ana entra con la contraseña nueva sin esperar a que se recargue el límite.
	w := testutil.DoFromIP(t, api, addr.next(), nil, http.MethodPost, "/auth/login",
		fmt.Sprintf(`{"email":"ana@ejemplo.com","password":%q}`, newPassword))
	testutil.Decode[map[string]bool](t, w, http.StatusAccepted)
	var mine *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == auth.ChallengeCookieName {
			mine = c
		}
	}
	if w := challengeFromIP(t, api, addr.next(), mine, codeBody(tf.Code(t))); w.Code != http.StatusOK {
		t.Errorf("login de Ana después del reset: status = %d; cuerpo: %s", w.Code, w.Body)
	}
}

func TestPasswordChangeEndsPendingChallenges(t *testing.T) {
	pool := testutil.NewDB(t)
	api := testutil.NewAPI(pool)
	_, session := testutil.Login(t, pool, "ana@ejemplo.com", auth.RoleUser)
	tf := testutil.EnableTwoFactor(t, api, session, testutil.Password)
	addr := &ips{}

	// Alguien con la contraseña pasó el primer paso del login…
	attacker := loginFromIP(t, api, addr.next())

	// …y Ana, que tiene su sesión abierta, cambia la contraseña.
	body := fmt.Sprintf(`{"current_password":"contraseña-de-prueba","password":%q}`, newPassword)
	if w := testutil.DoFromIP(t, api, addr.next(), session, http.MethodPut, "/auth/password", body); w.Code != http.StatusNoContent {
		t.Fatalf("cambio de contraseña: status = %d; cuerpo: %s", w.Code, w.Body)
	}

	testutil.Decode[testutil.APIError](t, challengeFromIP(t, api, addr.next(), attacker, codeBody(tf.Code(t))), http.StatusUnauthorized)
}
