package auth_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/NoeOnDev/go-api-base/internal/auth"
	"github.com/NoeOnDev/go-api-base/internal/mail"
	"github.com/NoeOnDev/go-api-base/internal/testutil"
)

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
		if resp.Error.Code != "too_many_requests" || w.Header().Get("Retry-After") != "1200" {
			t.Errorf("respuesta inesperada: %+v, Retry-After %q", resp.Error, w.Header().Get("Retry-After"))
		}
	}

	if n := len(mails.Messages()); n != 4 {
		t.Errorf("se enviaron %d correos, se esperaban 4 (registro y 3 reenvíos)", n)
	}
}
