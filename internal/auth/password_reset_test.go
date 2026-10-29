package auth_test

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/NoeOnDev/go-api-base/internal/auth"
	"github.com/NoeOnDev/go-api-base/internal/mail"
	"github.com/NoeOnDev/go-api-base/internal/testutil"
)

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
	service := auth.NewService(pool, auth.Options{})
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

// Si un paso de la recuperación falla, no cambia nada y el enlace sigue sirviendo.
func TestResetPasswordIsAtomic(t *testing.T) {
	pool := testutil.NewDB(t)
	mails := &mail.Recorder{}
	api := testutil.NewAPI(pool, testutil.WithMailer(mails))
	testutil.Login(t, pool, "ana@ejemplo.com", auth.RoleUser) // una sesión que cerrar

	testutil.Do(t, api, nil, http.MethodPost, "/auth/password/forgot", `{"email":"ana@ejemplo.com"}`)
	msg, _ := mails.Last("ana@ejemplo.com")
	body := resetBody(testutil.TokenFromEmail(t, msg), newPassword)

	exec := func(sql string) error { _, err := pool.Exec(context.Background(), sql); return err }
	failSessionDeletes(t, exec)

	// Falla el cierre de sesiones, el último paso: se deshace todo.
	testutil.Decode[testutil.APIError](t, testutil.Do(t, api, nil, http.MethodPost, "/auth/password/reset", body), http.StatusInternalServerError)

	service := auth.NewService(pool, auth.Options{})
	if _, err := service.Authenticate(t.Context(), "ana@ejemplo.com", "contraseña-de-prueba"); err != nil {
		t.Errorf("la contraseña no debería haber cambiado: %v", err)
	}

	// Sin la falla, el mismo enlace funciona: no se gastó.
	if err := exec("DROP TRIGGER test_fail_session_delete ON sessions"); err != nil {
		t.Fatal(err)
	}
	if w := testutil.Do(t, api, nil, http.MethodPost, "/auth/password/reset", body); w.Code != http.StatusNoContent {
		t.Fatalf("el enlace debería seguir sirviendo: status = %d; cuerpo: %s", w.Code, w.Body)
	}
	if _, err := service.Authenticate(t.Context(), "ana@ejemplo.com", newPassword); err != nil {
		t.Errorf("la contraseña nueva debería funcionar: %v", err)
	}
}

// Varios pedidos de enlace simultáneos para el mismo usuario dejan un solo enlace válido.
// (Sin el bloqueo de la fila del usuario, este test fallaría o no según coincidieran en el
// tiempo; con el bloqueo pasa siempre.)
func TestConcurrentForgotLeavesOneToken(t *testing.T) {
	pool := testutil.NewDB(t)
	testutil.Login(t, pool, "ana@ejemplo.com", auth.RoleUser)

	for round := range 5 {
		api := testutil.NewAPI(pool) // límites nuevos en cada ronda
		var wg sync.WaitGroup
		for i := range 3 {
			wg.Go(func() {
				ip := fmt.Sprintf("198.51.%d.%d", round, i+1)
				testutil.DoFromIP(t, api, ip, nil, http.MethodPost, "/auth/password/forgot", `{"email":"ana@ejemplo.com"}`)
			})
		}
		wg.Wait()

		var n int
		if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM user_tokens WHERE purpose = 'password_reset'").Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 1 {
			t.Fatalf("ronda %d: quedaron %d enlaces válidos, se esperaba 1", round+1, n)
		}
	}
}

// Los límites por destinatario frenan a quien pide correos para otra persona desde muchas IPs:
// cada petición sale de una IP distinta, así que el límite por IP no interviene.
func TestForgotPasswordRecipientLimit(t *testing.T) {
	pool := testutil.NewDB(t)
	mails := &mail.Recorder{}
	api := testutil.NewAPI(pool, testutil.WithMailer(mails))
	testutil.Login(t, pool, "ana@ejemplo.com", auth.RoleUser)

	for i := range 4 {
		ip := fmt.Sprintf("198.51.100.%d", i+1)
		w := testutil.DoFromIP(t, api, ip, nil, http.MethodPost, "/auth/password/forgot", `{"email":"ana@ejemplo.com"}`)
		// Siempre la misma respuesta, para no revelar nada.
		testutil.Decode[map[string]string](t, w, http.StatusAccepted)
	}

	if n := len(mails.Messages()); n != 3 {
		t.Fatalf("se enviaron %d correos, se esperaban 3", n)
	}

	// El cuarto pedido no invalidó el último enlace enviado.
	last, _ := mails.Last("ana@ejemplo.com")
	w := testutil.Do(t, api, nil, http.MethodPost, "/auth/password/reset", resetBody(testutil.TokenFromEmail(t, last), newPassword))
	if w.Code != http.StatusNoContent {
		t.Errorf("el último enlace debería seguir sirviendo: status = %d; cuerpo: %s", w.Code, w.Body)
	}
}
