package auth_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/NoeOnDev/go-api-base/internal/auth"
	"github.com/NoeOnDev/go-api-base/internal/mail"
	"github.com/NoeOnDev/go-api-base/internal/testutil"
)

func verifyBody(token string) string { return fmt.Sprintf(`{"token":%q}`, token) }

func TestEmailVerificationRequired(t *testing.T) {
	pool := testutil.NewDB(t)
	mails := &mail.Recorder{}
	api := testutil.NewAPI(pool, testutil.WithMailer(mails), testutil.WithConfig(requireVerification))

	// Al registrarse se envía el enlace, y el usuario queda sin verificar.
	w := testutil.Do(t, api, nil, http.MethodPost, "/auth/register", registerBody)
	u := testutil.Decode[auth.User](t, w, http.StatusCreated)
	if u.EmailVerified() {
		t.Fatal("el usuario no debería estar verificado al registrarse")
	}
	cookie := sessionCookie(t, w)

	first, ok := mails.Last("alice@example.com")
	if !ok || !strings.Contains(first.Body, "https://app.test/verify-email?token=") {
		t.Fatalf("no se envió el enlace de verificación: %+v", mails.Messages())
	}

	// Sin verificar: las rutas protegidas responden 403, pero la cuenta sigue accesible.
	resp := testutil.Decode[testutil.APIError](t, testutil.Do(t, api, cookie, http.MethodGet, "/notes", ""), http.StatusForbidden)
	if resp.Error.Code != "email_not_verified" {
		t.Errorf("code = %q", resp.Error.Code)
	}
	for _, tt := range []struct{ method, path, body string }{
		{http.MethodGet, "/auth/me", ""},
		{http.MethodPut, "/auth/me", `{"name":"Alice"}`},
	} {
		if w := testutil.Do(t, api, cookie, tt.method, tt.path, tt.body); w.Code != http.StatusOK {
			t.Errorf("%s %s sin verificar: status = %d", tt.method, tt.path, w.Code)
		}
	}

	// Pedir otro enlace invalida el anterior.
	if w := testutil.Do(t, api, cookie, http.MethodPost, "/auth/email/resend", ""); w.Code != http.StatusAccepted {
		t.Fatalf("resend: status = %d", w.Code)
	}
	second, _ := mails.Last("alice@example.com")
	testutil.Decode[testutil.APIError](t,
		testutil.Do(t, api, nil, http.MethodPost, "/auth/email/verify", verifyBody(testutil.TokenFromEmail(t, first))),
		http.StatusUnprocessableEntity)

	// Con el enlace nuevo se verifica, y las rutas protegidas ya funcionan.
	if w := testutil.Do(t, api, nil, http.MethodPost, "/auth/email/verify", verifyBody(testutil.TokenFromEmail(t, second))); w.Code != http.StatusNoContent {
		t.Fatalf("verify: status = %d; cuerpo: %s", w.Code, w.Body)
	}
	me := testutil.Decode[auth.User](t, testutil.Do(t, api, cookie, http.MethodGet, "/auth/me", ""), http.StatusOK)
	if !me.EmailVerified() {
		t.Error("el email no quedó verificado")
	}
	if w := testutil.Do(t, api, cookie, http.MethodGet, "/notes", ""); w.Code != http.StatusOK {
		t.Errorf("/notes después de verificar: status = %d", w.Code)
	}

	// Ya verificado, pedir otro enlace no envía nada.
	sent := len(mails.Messages())
	if w := testutil.Do(t, api, cookie, http.MethodPost, "/auth/email/resend", ""); w.Code != http.StatusNoContent {
		t.Errorf("resend ya verificado: status = %d", w.Code)
	}
	if len(mails.Messages()) != sent {
		t.Error("se envió un correo a un email ya verificado")
	}
}

func TestEmailVerificationDisabledByDefault(t *testing.T) {
	pool := testutil.NewDB(t)
	mails := &mail.Recorder{}
	api := testutil.NewAPI(pool, testutil.WithMailer(mails))

	w := testutil.Do(t, api, nil, http.MethodPost, "/auth/register", registerBody)
	testutil.Decode[auth.User](t, w, http.StatusCreated)

	if len(mails.Messages()) != 0 {
		t.Errorf("no debería enviar correos al registrarse: %+v", mails.Messages())
	}
	if w := testutil.Do(t, api, sessionCookie(t, w), http.MethodGet, "/notes", ""); w.Code != http.StatusOK {
		t.Errorf("sin verificación obligatoria, /notes debería funcionar: status = %d", w.Code)
	}
}

func TestAdminsAreVerified(t *testing.T) {
	pool := testutil.NewDB(t)
	api := testutil.NewAPI(pool, testutil.WithConfig(requireVerification))
	admin, cookie := testutil.Login(t, pool, "admin@example.com", auth.RoleAdmin)

	if !admin.EmailVerified() {
		t.Error("un admin debería quedar verificado al crearse")
	}
	if w := testutil.Do(t, api, cookie, http.MethodPost, "/categories", `{"name":"Bebidas","slug":"bebidas"}`); w.Code != http.StatusCreated {
		t.Errorf("el admin no puede usar rutas protegidas: status = %d", w.Code)
	}
}

func TestVerifyEmailInvalidToken(t *testing.T) {
	pool := testutil.NewDB(t)
	api := testutil.NewAPI(pool)

	resp := testutil.Decode[testutil.APIError](t,
		testutil.Do(t, api, nil, http.MethodPost, "/auth/email/verify", verifyBody("token-inventado")),
		http.StatusUnprocessableEntity)
	if resp.Error.Fields["token"] == "" {
		t.Errorf("se esperaba error en token: %+v", resp.Error)
	}
	testutil.Decode[testutil.APIError](t, testutil.Do(t, api, nil, http.MethodPost, "/auth/email/resend", ""), http.StatusUnauthorized)
}

// Los límites por destinatario frenan a quien pide correos para otra persona desde muchas IPs:
// cada petición sale de una IP distinta, así que el límite por IP no interviene.
func TestResendVerificationRecipientLimit(t *testing.T) {
	pool := testutil.NewDB(t)
	mails := &mail.Recorder{}
	api := testutil.NewAPI(pool, testutil.WithMailer(mails), testutil.WithConfig(requireVerification))

	w := testutil.Do(t, api, nil, http.MethodPost, "/auth/register", registerBody)
	testutil.Decode[auth.User](t, w, http.StatusCreated)
	cookie := sessionCookie(t, w)

	// El correo del registro no cuenta: después se pueden pedir 3 más.
	for i := range 4 {
		ip := fmt.Sprintf("198.51.100.%d", i+1)
		w := testutil.DoFromIP(t, api, ip, cookie, http.MethodPost, "/auth/email/resend", "")
		if i < 3 {
			if w.Code != http.StatusAccepted {
				t.Fatalf("resend %d: status = %d", i+1, w.Code)
			}
			continue
		}
		resp := testutil.Decode[testutil.APIError](t, w, http.StatusTooManyRequests)
		if resp.Error.Code != "too_many_emails" || w.Header().Get("Retry-After") != "1200" {
			t.Errorf("respuesta inesperada: %+v, Retry-After %q", resp.Error, w.Header().Get("Retry-After"))
		}
	}

	if n := len(mails.Messages()); n != 4 {
		t.Errorf("se enviaron %d correos, se esperaban 4 (registro y 3 reenvíos)", n)
	}
}
