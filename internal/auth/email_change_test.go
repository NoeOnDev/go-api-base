package auth_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/NoeOnDev/go-api-base/internal/auth"
	"github.com/NoeOnDev/go-api-base/internal/mail"
	"github.com/NoeOnDev/go-api-base/internal/testutil"
)

const changeToNew = `{"email":"Alice.New@Example.com","password":"test-password"}`

func confirmBody(token string) string { return `{"token":"` + token + `"}` }

func TestChangeEmail(t *testing.T) {
	pool := testutil.NewDB(t)
	mails := &mail.Recorder{}
	api := testutil.NewAPI(pool, testutil.WithMailer(mails))
	_, cookie := testutil.Login(t, pool, "alice@example.com", auth.RoleUser)

	if w := testutil.Do(t, api, cookie, http.MethodPut, "/auth/email", changeToNew); w.Code != http.StatusAccepted {
		t.Fatalf("pedir el cambio: status = %d; cuerpo: %s", w.Code, w.Body)
	}

	// El enlace va al email nuevo; al actual, el aviso.
	link, ok := mails.Last("alice.new@example.com")
	if !ok || !strings.Contains(link.Body, "https://app.test/confirm-email?token=") {
		t.Fatalf("no llegó el enlace al email nuevo: %+v", mails.Messages())
	}
	notice, ok := mails.Last("alice@example.com")
	if !ok || !strings.Contains(notice.Body, "alice.new@example.com") || strings.Contains(notice.Body, "token=") {
		t.Fatalf("el aviso al email actual: %+v", notice)
	}

	// Hasta confirmar, el email no cambia.
	if u := testutil.Decode[auth.User](t, testutil.Do(t, api, cookie, http.MethodGet, "/auth/me", ""), http.StatusOK); u.Email != "alice@example.com" {
		t.Fatalf("el email cambió antes de confirmar: %s", u.Email)
	}

	token := testutil.TokenFromEmail(t, link)
	if w := testutil.Do(t, api, nil, http.MethodPost, "/auth/email/confirm", confirmBody(token)); w.Code != http.StatusNoContent {
		t.Fatalf("confirmar: status = %d; cuerpo: %s", w.Code, w.Body)
	}
	u := testutil.Decode[auth.User](t, testutil.Do(t, api, cookie, http.MethodGet, "/auth/me", ""), http.StatusOK)
	if u.Email != "alice.new@example.com" || !u.EmailVerified() {
		t.Errorf("después de confirmar: %+v", u)
	}

	// El login usa el email nuevo, y el enlace sirve una sola vez.
	service := auth.NewService(pool, auth.Options{})
	if _, err := service.Authenticate(t.Context(), "alice.new@example.com", "test-password"); err != nil {
		t.Errorf("login con el email nuevo: %v", err)
	}
	if _, err := service.Authenticate(t.Context(), "alice@example.com", "test-password"); err == nil {
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
	_, cookie := testutil.Login(t, pool, "alice@example.com", auth.RoleUser)
	testutil.Login(t, pool, "bob@example.com", auth.RoleUser)

	for name, tt := range map[string]struct {
		body   string
		status int
	}{
		"sin contraseña":        {`{"email":"new@example.com"}`, http.StatusUnprocessableEntity},
		"contraseña incorrecta": {`{"email":"new@example.com","password":"other"}`, http.StatusUnprocessableEntity},
		"email inválido":        {`{"email":"no-es-email","password":"test-password"}`, http.StatusUnprocessableEntity},
		"el mismo email":        {`{"email":"ALICE@example.com","password":"test-password"}`, http.StatusUnprocessableEntity},
		"email de otra cuenta":  {`{"email":"bob@example.com","password":"test-password"}`, http.StatusConflict},
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
	_, cookie := testutil.Login(t, pool, "alice@example.com", auth.RoleUser)

	testutil.Do(t, api, cookie, http.MethodPut, "/auth/email", changeToNew)
	link, _ := mails.Last("alice.new@example.com")

	testutil.Login(t, pool, "alice.new@example.com", auth.RoleUser) // otra cuenta se registra con ese email

	testutil.Decode[testutil.APIError](t, testutil.Do(t, api, nil, http.MethodPost, "/auth/email/confirm", confirmBody(testutil.TokenFromEmail(t, link))), http.StatusConflict)
	if u := testutil.Decode[auth.User](t, testutil.Do(t, api, cookie, http.MethodGet, "/auth/me", ""), http.StatusOK); u.Email != "alice@example.com" {
		t.Errorf("el email no debería haber cambiado: %s", u.Email)
	}
}

// Confirmar el email nuevo anula los enlaces de recuperación enviados al anterior: si el usuario
// cambió el email porque perdió el control del viejo, quien lo tenga no puede usarlos.
func TestConfirmEmailChangeRevokesPasswordReset(t *testing.T) {
	pool := testutil.NewDB(t)
	mails := &mail.Recorder{}
	api := testutil.NewAPI(pool, testutil.WithMailer(mails))
	_, cookie := testutil.Login(t, pool, "alice@example.com", auth.RoleUser)

	testutil.Do(t, api, nil, http.MethodPost, "/auth/password/forgot", `{"email":"alice@example.com"}`)
	reset, ok := mails.Last("alice@example.com")
	if !ok {
		t.Fatal("no llegó el enlace de recuperación")
	}
	resetToken := testutil.TokenFromEmail(t, reset)

	testutil.Do(t, api, cookie, http.MethodPut, "/auth/email", changeToNew)
	link, _ := mails.Last("alice.new@example.com")
	if w := testutil.Do(t, api, nil, http.MethodPost, "/auth/email/confirm", confirmBody(testutil.TokenFromEmail(t, link))); w.Code != http.StatusNoContent {
		t.Fatalf("confirmar: status = %d; cuerpo: %s", w.Code, w.Body)
	}

	testutil.Decode[testutil.APIError](t,
		testutil.Do(t, api, nil, http.MethodPost, "/auth/password/reset", resetBody(resetToken, newPassword)),
		http.StatusUnprocessableEntity)
	service := auth.NewService(pool, auth.Options{})
	if _, err := service.Authenticate(t.Context(), "alice.new@example.com", "test-password"); err != nil {
		t.Errorf("la contraseña no debería haber cambiado: %v", err)
	}
}

// Cambiar o recuperar la contraseña cancela un cambio de email pendiente: lo pudo pedir quien
// conocía la contraseña anterior, para quedarse con la cuenta.
func TestPasswordChangeCancelsEmailChange(t *testing.T) {
	// requestChange pide el cambio de email y devuelve el token del enlace.
	requestChange := func(t *testing.T, api http.Handler, mails *mail.Recorder, cookie *http.Cookie) string {
		t.Helper()
		if w := testutil.Do(t, api, cookie, http.MethodPut, "/auth/email", changeToNew); w.Code != http.StatusAccepted {
			t.Fatalf("pedir el cambio: status = %d; cuerpo: %s", w.Code, w.Body)
		}
		link, _ := mails.Last("alice.new@example.com")
		return testutil.TokenFromEmail(t, link)
	}

	t.Run("al cambiar la contraseña", func(t *testing.T) {
		pool := testutil.NewDB(t)
		mails := &mail.Recorder{}
		api := testutil.NewAPI(pool, testutil.WithMailer(mails))
		_, cookie := testutil.Login(t, pool, "alice@example.com", auth.RoleUser)

		token := requestChange(t, api, mails, cookie)
		body := `{"current_password":"test-password","password":"` + newPassword + `"}`
		if w := testutil.Do(t, api, cookie, http.MethodPut, "/auth/password", body); w.Code != http.StatusNoContent {
			t.Fatalf("cambiar la contraseña: status = %d; cuerpo: %s", w.Code, w.Body)
		}

		testutil.Decode[testutil.APIError](t, testutil.Do(t, api, nil, http.MethodPost, "/auth/email/confirm", confirmBody(token)), http.StatusUnprocessableEntity)
		if u := testutil.Decode[auth.User](t, testutil.Do(t, api, cookie, http.MethodGet, "/auth/me", ""), http.StatusOK); u.Email != "alice@example.com" {
			t.Errorf("el email no debería haber cambiado: %s", u.Email)
		}
	})

	t.Run("al recuperar la contraseña", func(t *testing.T) {
		pool := testutil.NewDB(t)
		mails := &mail.Recorder{}
		api := testutil.NewAPI(pool, testutil.WithMailer(mails))
		_, cookie := testutil.Login(t, pool, "alice@example.com", auth.RoleUser)

		token := requestChange(t, api, mails, cookie)
		testutil.Do(t, api, nil, http.MethodPost, "/auth/password/forgot", `{"email":"alice@example.com"}`)
		reset, _ := mails.Last("alice@example.com")
		if w := testutil.Do(t, api, nil, http.MethodPost, "/auth/password/reset", resetBody(testutil.TokenFromEmail(t, reset), newPassword)); w.Code != http.StatusNoContent {
			t.Fatalf("recuperar la contraseña: status = %d; cuerpo: %s", w.Code, w.Body)
		}

		testutil.Decode[testutil.APIError](t, testutil.Do(t, api, nil, http.MethodPost, "/auth/email/confirm", confirmBody(token)), http.StatusUnprocessableEntity)
		service := auth.NewService(pool, auth.Options{})
		if _, err := service.Authenticate(t.Context(), "alice@example.com", newPassword); err != nil {
			t.Errorf("el email no debería haber cambiado: %v", err)
		}
	})
}
