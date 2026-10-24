package auth_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/NoeOnDev/go-api-base/internal/auth"
	"github.com/NoeOnDev/go-api-base/internal/mail"
	"github.com/NoeOnDev/go-api-base/internal/store"
	"github.com/NoeOnDev/go-api-base/internal/testutil"
)

const newPassword = "una-clave-nueva"

func resetBody(token, password string) string {
	return fmt.Sprintf(`{"token":%q,"password":%q}`, token, password)
}

func TestForgotPasswordDoesNotRevealEmails(t *testing.T) {
	pool := testutil.NewDB(t)
	mails := &mail.Recorder{}
	api := testutil.NewAPI(pool, testutil.WithMailer(mails))
	testutil.Login(t, pool, "ana@ejemplo.com", auth.RoleUser)

	unknown := testutil.Do(t, api, nil, http.MethodPost, "/auth/password/forgot", `{"email":"nadie@ejemplo.com"}`)
	known := testutil.Do(t, api, nil, http.MethodPost, "/auth/password/forgot", `{"email":" ANA@ejemplo.com "}`)

	if unknown.Code != http.StatusAccepted || known.Code != http.StatusAccepted {
		t.Fatalf("status = %d y %d, se esperaba 202", unknown.Code, known.Code)
	}
	if unknown.Body.String() != known.Body.String() {
		t.Errorf("las respuestas difieren:\n%s\n%s", unknown.Body, known.Body)
	}

	// Solo se envió un correo, al email registrado, con el enlace al frontend.
	msgs := mails.Messages()
	if len(msgs) != 1 || msgs[0].To != "ana@ejemplo.com" {
		t.Fatalf("correos enviados: %+v", msgs)
	}
	if !strings.Contains(msgs[0].Body, "https://app.test/reset-password?token=") {
		t.Errorf("el correo no tiene el enlace esperado:\n%s", msgs[0].Body)
	}

	resp := testutil.Decode[testutil.APIError](t,
		testutil.Do(t, api, nil, http.MethodPost, "/auth/password/forgot", `{"email":"no-es-email"}`),
		http.StatusUnprocessableEntity)
	if resp.Error.Fields["email"] == "" {
		t.Errorf("se esperaba error en email: %+v", resp.Error)
	}
}

func TestResetPassword(t *testing.T) {
	pool := testutil.NewDB(t)
	mails := &mail.Recorder{}
	api := testutil.NewAPI(pool, testutil.WithMailer(mails))
	user, session := testutil.Login(t, pool, "ana@ejemplo.com", auth.RoleUser)

	testutil.Do(t, api, nil, http.MethodPost, "/auth/password/forgot", `{"email":"ana@ejemplo.com"}`)
	msg, _ := mails.Last("ana@ejemplo.com")
	token := testutil.TokenFromEmail(t, msg)

	t.Run("una contraseña inválida no gasta el token", func(t *testing.T) {
		resp := testutil.Decode[testutil.APIError](t,
			testutil.Do(t, api, nil, http.MethodPost, "/auth/password/reset", resetBody(token, "corta")),
			http.StatusUnprocessableEntity)
		if resp.Error.Fields["password"] == "" {
			t.Errorf("se esperaba error en password: %+v", resp.Error)
		}
	})

	if w := testutil.Do(t, api, nil, http.MethodPost, "/auth/password/reset", resetBody(token, newPassword)); w.Code != http.StatusNoContent {
		t.Fatalf("reset: status = %d; cuerpo: %s", w.Code, w.Body)
	}

	// Se cerraron todas las sesiones del usuario.
	if w := testutil.Do(t, api, session, http.MethodGet, "/auth/me", ""); w.Code != http.StatusUnauthorized {
		t.Errorf("la sesión anterior sigue activa: status = %d", w.Code)
	}

	// La contraseña cambió y el email quedó verificado (el usuario recibió el correo).
	service := auth.NewService(store.New(pool), auth.Options{})
	u, err := service.Authenticate(t.Context(), "ana@ejemplo.com", newPassword)
	if err != nil {
		t.Fatalf("login con la contraseña nueva: %v", err)
	}
	if !u.EmailVerified() || u.ID != user.ID {
		t.Errorf("usuario inesperado: %+v", u)
	}

	// El token sirve una sola vez.
	resp := testutil.Decode[testutil.APIError](t,
		testutil.Do(t, api, nil, http.MethodPost, "/auth/password/reset", resetBody(token, "otra-clave-larga")),
		http.StatusUnprocessableEntity)
	if resp.Error.Fields["token"] == "" {
		t.Errorf("se esperaba error en token: %+v", resp.Error)
	}
}

func TestResetPasswordInvalidTokens(t *testing.T) {
	pool := testutil.NewDB(t)
	mails := &mail.Recorder{}
	api := testutil.NewAPI(pool, testutil.WithMailer(mails))
	testutil.Login(t, pool, "ana@ejemplo.com", auth.RoleUser)

	forgot := func() string {
		testutil.Do(t, api, nil, http.MethodPost, "/auth/password/forgot", `{"email":"ana@ejemplo.com"}`)
		msg, _ := mails.Last("ana@ejemplo.com")
		return testutil.TokenFromEmail(t, msg)
	}

	first := forgot()
	second := forgot() // pedir otro enlace invalida el anterior

	expired := second
	if _, err := pool.Exec(t.Context(), "UPDATE user_tokens SET expires_at = now() - interval '1 minute'"); err != nil {
		t.Fatal(err)
	}

	for name, token := range map[string]string{
		"inventado":            "token-inventado",
		"vacío":                "",
		"reemplazado por otro": first,
		"vencido":              expired,
	} {
		t.Run(name, func(t *testing.T) {
			testutil.Decode[testutil.APIError](t,
				testutil.Do(t, api, nil, http.MethodPost, "/auth/password/reset", resetBody(token, newPassword)),
				http.StatusUnprocessableEntity)
		})
	}
}
