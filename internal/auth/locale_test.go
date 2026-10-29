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
		`{"name":"Alice","email":"alice@example.com","password":"a-long-password"}`)
	if u := testutil.Decode[auth.User](t, w, http.StatusCreated); u.Locale == nil || *u.Locale != "es-MX" {
		t.Fatalf("locale = %v, se esperaba es-MX", u.Locale)
	}
	// Sin Accept-Language queda null: sigue al idioma por defecto aunque cambie.
	w = testutil.Do(t, api, nil, http.MethodPost, "/auth/register",
		`{"name":"Bob","email":"bob@example.com","password":"a-long-password"}`)
	if u := testutil.Decode[auth.User](t, w, http.StatusCreated); u.Locale != nil {
		t.Errorf("sin Accept-Language: locale = %q, se esperaba null", *u.Locale)
	}

	// La petición llega sin idioma (en inglés), pero el correo va en el de Alice.
	w = testutil.Do(t, api, nil, http.MethodPost, "/auth/password/forgot", `{"email":"alice@example.com"}`)
	if resp := testutil.Decode[map[string]string](t, w, http.StatusAccepted); !strings.HasPrefix(resp["message"], "If the email is registered") {
		t.Errorf("la respuesta va en el idioma de la petición: %q", resp["message"])
	}
	msg, ok := mails.Last("alice@example.com")
	if !ok || msg.Subject != "Recupera tu contraseña" || !strings.Contains(msg.Body, "vence en 1 hora") {
		t.Errorf("correo en español: %+v", msg)
	}
	testutil.Do(t, api, nil, http.MethodPost, "/auth/password/forgot", `{"email":"bob@example.com"}`)
	if msg, ok := mails.Last("bob@example.com"); !ok || msg.Subject != "Reset your password" || !strings.Contains(msg.Body, "expires in 1 hour") {
		t.Errorf("correo en el idioma por defecto: %+v", msg)
	}
}

func TestUpdateLocale(t *testing.T) {
	pool := testutil.NewDB(t)
	api := testutil.NewAPI(pool)
	_, cookie := testutil.Login(t, pool, "alice@example.com", auth.RoleUser)

	// Se acepta escrito en cualquier forma, y se guarda como el catálogo.
	w := testutil.Do(t, api, cookie, http.MethodPut, "/auth/me", `{"name":"Alice","locale":"es-mx"}`)
	if u := testutil.Decode[auth.User](t, w, http.StatusOK); u.Locale == nil || *u.Locale != "es-MX" {
		t.Errorf("locale = %v", u.Locale)
	}
	// Sin locale, no cambia.
	w = testutil.Do(t, api, cookie, http.MethodPut, "/auth/me", `{"name":"Alice Smith"}`)
	if u := testutil.Decode[auth.User](t, w, http.StatusOK); u.Locale == nil || *u.Locale != "es-MX" {
		t.Errorf("después de cambiar solo el nombre: locale = %v", u.Locale)
	}
	// Un idioma sin catálogo se rechaza, aunque haya uno cercano: el usuario elige uno de la lista.
	w = testutil.DoWithLanguage(t, api, "es-MX", cookie, http.MethodPut, "/auth/me", `{"name":"Alice","locale":"es-AR"}`)
	resp := testutil.Decode[testutil.APIError](t, w, http.StatusUnprocessableEntity)
	if resp.Error.Fields["locale"] != "Debe ser uno de estos: en, es-MX." {
		t.Errorf("locale inválido: %+v", resp.Error)
	}
}
