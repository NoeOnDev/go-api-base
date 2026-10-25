package auth_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/NoeOnDev/go-api-base/internal/auth"
	"github.com/NoeOnDev/go-api-base/internal/mail"
	"github.com/NoeOnDev/go-api-base/internal/testutil"
)

const changeToNew = `{"email":"Ana.Nueva@Ejemplo.com","password":"contraseña-de-prueba"}`

func confirmBody(token string) string { return `{"token":"` + token + `"}` }

func TestChangeEmail(t *testing.T) {
	pool := testutil.NewDB(t)
	mails := &mail.Recorder{}
	api := testutil.NewAPI(pool, testutil.WithMailer(mails))
	_, cookie := testutil.Login(t, pool, "ana@ejemplo.com", auth.RoleUser)

	if w := testutil.Do(t, api, cookie, http.MethodPut, "/auth/email", changeToNew); w.Code != http.StatusAccepted {
		t.Fatalf("pedir el cambio: status = %d; cuerpo: %s", w.Code, w.Body)
	}

	// El enlace va al email nuevo; al actual, el aviso.
	link, ok := mails.Last("ana.nueva@ejemplo.com")
	if !ok || !strings.Contains(link.Body, "https://app.test/confirm-email?token=") {
		t.Fatalf("no llegó el enlace al email nuevo: %+v", mails.Messages())
	}
	notice, ok := mails.Last("ana@ejemplo.com")
	if !ok || !strings.Contains(notice.Body, "ana.nueva@ejemplo.com") || strings.Contains(notice.Body, "token=") {
		t.Fatalf("el aviso al email actual: %+v", notice)
	}

	// Hasta confirmar, el email no cambia.
	if u := testutil.Decode[auth.User](t, testutil.Do(t, api, cookie, http.MethodGet, "/auth/me", ""), http.StatusOK); u.Email != "ana@ejemplo.com" {
		t.Fatalf("el email cambió antes de confirmar: %s", u.Email)
	}

	token := testutil.TokenFromEmail(t, link)
	if w := testutil.Do(t, api, nil, http.MethodPost, "/auth/email/confirm", confirmBody(token)); w.Code != http.StatusNoContent {
		t.Fatalf("confirmar: status = %d; cuerpo: %s", w.Code, w.Body)
	}
	u := testutil.Decode[auth.User](t, testutil.Do(t, api, cookie, http.MethodGet, "/auth/me", ""), http.StatusOK)
	if u.Email != "ana.nueva@ejemplo.com" || !u.EmailVerified() {
		t.Errorf("después de confirmar: %+v", u)
	}

	// El login usa el email nuevo, y el enlace sirve una sola vez.
	service := auth.NewService(pool, auth.Options{})
	if _, err := service.Authenticate(t.Context(), "ana.nueva@ejemplo.com", "contraseña-de-prueba"); err != nil {
		t.Errorf("login con el email nuevo: %v", err)
	}
	if _, err := service.Authenticate(t.Context(), "ana@ejemplo.com", "contraseña-de-prueba"); err == nil {
		t.Error("el email viejo no debería servir más")
	}
	testutil.Decode[testutil.APIError](t, testutil.Do(t, api, nil, http.MethodPost, "/auth/email/confirm", confirmBody(token)), http.StatusUnprocessableEntity)

	// Y queda la notificación.
	var kind string
	if err := pool.QueryRow(t.Context(), "SELECT kind FROM notifications ORDER BY id DESC LIMIT 1").Scan(&kind); err != nil || kind != auth.NotifyEmailChanged {
		t.Errorf("notificación: %q (%v)", kind, err)
	}
}

func TestChangeEmailRejects(t *testing.T) {
	pool := testutil.NewDB(t)
	api := testutil.NewAPI(pool)
	_, cookie := testutil.Login(t, pool, "ana@ejemplo.com", auth.RoleUser)
	testutil.Login(t, pool, "luis@ejemplo.com", auth.RoleUser)

	for name, tt := range map[string]struct {
		body   string
		status int
	}{
		"sin contraseña":        {`{"email":"nuevo@ejemplo.com"}`, http.StatusUnprocessableEntity},
		"contraseña incorrecta": {`{"email":"nuevo@ejemplo.com","password":"otra"}`, http.StatusUnprocessableEntity},
		"email inválido":        {`{"email":"no-es-email","password":"contraseña-de-prueba"}`, http.StatusUnprocessableEntity},
		"el mismo email":        {`{"email":"ANA@ejemplo.com","password":"contraseña-de-prueba"}`, http.StatusUnprocessableEntity},
		"email de otra cuenta":  {`{"email":"luis@ejemplo.com","password":"contraseña-de-prueba"}`, http.StatusConflict},
	} {
		if w := testutil.Do(t, api, cookie, http.MethodPut, "/auth/email", tt.body); w.Code != tt.status {
			t.Errorf("%s: status = %d, se esperaba %d", name, w.Code, tt.status)
		}
	}
	testutil.Decode[testutil.APIError](t, testutil.Do(t, api, nil, http.MethodPut, "/auth/email", changeToNew), http.StatusUnauthorized)
}

// Si mientras tanto otra cuenta tomó el email nuevo, la confirmación responde 409 y no cambia nada.
func TestConfirmEmailTakenMeanwhile(t *testing.T) {
	pool := testutil.NewDB(t)
	mails := &mail.Recorder{}
	api := testutil.NewAPI(pool, testutil.WithMailer(mails))
	_, cookie := testutil.Login(t, pool, "ana@ejemplo.com", auth.RoleUser)

	testutil.Do(t, api, cookie, http.MethodPut, "/auth/email", changeToNew)
	link, _ := mails.Last("ana.nueva@ejemplo.com")

	testutil.Login(t, pool, "ana.nueva@ejemplo.com", auth.RoleUser) // otra cuenta se registra con ese email

	testutil.Decode[testutil.APIError](t, testutil.Do(t, api, nil, http.MethodPost, "/auth/email/confirm", confirmBody(testutil.TokenFromEmail(t, link))), http.StatusConflict)
	if u := testutil.Decode[auth.User](t, testutil.Do(t, api, cookie, http.MethodGet, "/auth/me", ""), http.StatusOK); u.Email != "ana@ejemplo.com" {
		t.Errorf("el email no debería haber cambiado: %s", u.Email)
	}
}
