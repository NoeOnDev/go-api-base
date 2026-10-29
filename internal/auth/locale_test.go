package auth_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/NoeOnDev/go-api-base/internal/auth"
	"github.com/NoeOnDev/go-api-base/internal/mail"
	"github.com/NoeOnDev/go-api-base/internal/testutil"
)

// El registro guarda el idioma que pidió el navegador, y los correos de la cuenta salen en ese
// idioma aunque la petición que los dispara llegue en otro (por ejemplo, la de un atacante).
func TestUserLocale(t *testing.T) {
	pool := testutil.NewDB(t)
	mails := &mail.Recorder{}
	api := testutil.NewAPI(pool, testutil.WithMailer(mails))

	w := testutil.DoWithLanguage(t, api, "es-MX,es;q=0.9", nil, http.MethodPost, "/auth/register",
		`{"name":"Ana","email":"ana@ejemplo.com","password":"una-clave-larga"}`)
	if u := testutil.Decode[auth.User](t, w, http.StatusCreated); u.Locale == nil || *u.Locale != "es-MX" {
		t.Fatalf("locale = %v, se esperaba es-MX", u.Locale)
	}
	// Sin Accept-Language queda null: sigue al idioma por defecto aunque cambie.
	w = testutil.Do(t, api, nil, http.MethodPost, "/auth/register",
		`{"name":"Luis","email":"luis@ejemplo.com","password":"una-clave-larga"}`)
	if u := testutil.Decode[auth.User](t, w, http.StatusCreated); u.Locale != nil {
		t.Errorf("sin Accept-Language: locale = %q, se esperaba null", *u.Locale)
	}

	// La petición llega sin idioma (en inglés), pero el correo va en el de Ana.
	w = testutil.Do(t, api, nil, http.MethodPost, "/auth/password/forgot", `{"email":"ana@ejemplo.com"}`)
	if resp := testutil.Decode[map[string]string](t, w, http.StatusAccepted); !strings.HasPrefix(resp["message"], "If the email is registered") {
		t.Errorf("la respuesta va en el idioma de la petición: %q", resp["message"])
	}
	msg, ok := mails.Last("ana@ejemplo.com")
	if !ok || msg.Subject != "Recupera tu contraseña" || !strings.Contains(msg.Body, "vence en 1 hora") {
		t.Errorf("correo en español: %+v", msg)
	}
	testutil.Do(t, api, nil, http.MethodPost, "/auth/password/forgot", `{"email":"luis@ejemplo.com"}`)
	if msg, ok := mails.Last("luis@ejemplo.com"); !ok || msg.Subject != "Reset your password" || !strings.Contains(msg.Body, "expires in 1 hour") {
		t.Errorf("correo en el idioma por defecto: %+v", msg)
	}
}

func TestUpdateLocale(t *testing.T) {
	pool := testutil.NewDB(t)
	api := testutil.NewAPI(pool)
	_, cookie := testutil.Login(t, pool, "ana@ejemplo.com", auth.RoleUser)

	// Se acepta escrito en cualquier forma, y se guarda como el catálogo.
	w := testutil.Do(t, api, cookie, http.MethodPut, "/auth/me", `{"name":"Ana","locale":"es-mx"}`)
	if u := testutil.Decode[auth.User](t, w, http.StatusOK); u.Locale == nil || *u.Locale != "es-MX" {
		t.Errorf("locale = %v", u.Locale)
	}
	// Sin locale, no cambia.
	w = testutil.Do(t, api, cookie, http.MethodPut, "/auth/me", `{"name":"Ana María"}`)
	if u := testutil.Decode[auth.User](t, w, http.StatusOK); u.Locale == nil || *u.Locale != "es-MX" {
		t.Errorf("después de cambiar solo el nombre: locale = %v", u.Locale)
	}
	// Un idioma sin catálogo se rechaza, aunque haya uno cercano: el usuario elige uno de la lista.
	w = testutil.DoWithLanguage(t, api, "es-MX", cookie, http.MethodPut, "/auth/me", `{"name":"Ana","locale":"es-AR"}`)
	resp := testutil.Decode[testutil.APIError](t, w, http.StatusUnprocessableEntity)
	if resp.Error.Fields["locale"] != "Debe ser uno de estos: en, es-MX." {
		t.Errorf("locale inválido: %+v", resp.Error)
	}
}
