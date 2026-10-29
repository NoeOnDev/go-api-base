package auth_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/NoeOnDev/go-api-base/internal/auth"
	"github.com/NoeOnDev/go-api-base/internal/mail"
	"github.com/NoeOnDev/go-api-base/internal/testutil"
)

// Registration stores the language the browser asked for, and the account emails go out in that
// language even if the request that triggers them comes in another one (for example, an
// attacker's).
func TestUserLocale(t *testing.T) {
	pool := testutil.NewDB(t)
	mails := &mail.Recorder{}
	api := testutil.NewAPI(pool, testutil.WithMailer(mails))

	w := testutil.DoWithLanguage(t, api, "es-MX,es;q=0.9", nil, http.MethodPost, "/auth/register",
		`{"name":"Alice","email":"alice@example.com","password":"a-long-password"}`)
	if u := testutil.Decode[auth.User](t, w, http.StatusCreated); u.Locale == nil || *u.Locale != "es-MX" {
		t.Fatalf("locale = %v, want es-MX", u.Locale)
	}
	// Without Accept-Language it stays null: it follows the default language even if it changes.
	w = testutil.Do(t, api, nil, http.MethodPost, "/auth/register",
		`{"name":"Bob","email":"bob@example.com","password":"a-long-password"}`)
	if u := testutil.Decode[auth.User](t, w, http.StatusCreated); u.Locale != nil {
		t.Errorf("without Accept-Language: locale = %q, want null", *u.Locale)
	}

	// The request comes without a language (in English), but the email goes in Alice's.
	w = testutil.Do(t, api, nil, http.MethodPost, "/auth/password/forgot", `{"email":"alice@example.com"}`)
	if resp := testutil.Decode[map[string]string](t, w, http.StatusAccepted); !strings.HasPrefix(resp["message"], "If the email is registered") {
		t.Errorf("the response goes in the language of the request: %q", resp["message"])
	}
	msg, ok := mails.Last("alice@example.com")
	if !ok || msg.Subject != "Recupera tu contraseña" || !strings.Contains(msg.Body, "vence en 1 hora") {
		t.Errorf("email in Spanish: %+v", msg)
	}
	testutil.Do(t, api, nil, http.MethodPost, "/auth/password/forgot", `{"email":"bob@example.com"}`)
	if msg, ok := mails.Last("bob@example.com"); !ok || msg.Subject != "Reset your password" || !strings.Contains(msg.Body, "expires in 1 hour") {
		t.Errorf("email in the default language: %+v", msg)
	}
}

func TestUpdateLocale(t *testing.T) {
	pool := testutil.NewDB(t)
	api := testutil.NewAPI(pool)
	_, cookie := testutil.Login(t, pool, "alice@example.com", auth.RoleUser)

	// Any letter case is accepted, and it is stored as in the catalog.
	w := testutil.Do(t, api, cookie, http.MethodPut, "/auth/me", `{"name":"Alice","locale":"es-mx"}`)
	if u := testutil.Decode[auth.User](t, w, http.StatusOK); u.Locale == nil || *u.Locale != "es-MX" {
		t.Errorf("locale = %v", u.Locale)
	}
	// Without locale, it does not change.
	w = testutil.Do(t, api, cookie, http.MethodPut, "/auth/me", `{"name":"Alice Smith"}`)
	if u := testutil.Decode[auth.User](t, w, http.StatusOK); u.Locale == nil || *u.Locale != "es-MX" {
		t.Errorf("after changing only the name: locale = %v", u.Locale)
	}
	// A language without a catalog is rejected, even if there is a close one: the user picks one
	// from the list.
	w = testutil.DoWithLanguage(t, api, "es-MX", cookie, http.MethodPut, "/auth/me", `{"name":"Alice","locale":"es-AR"}`)
	resp := testutil.Decode[testutil.APIError](t, w, http.StatusUnprocessableEntity)
	if resp.Error.Fields["locale"] != "Debe ser uno de estos: en, es-MX." {
		t.Errorf("invalid locale: %+v", resp.Error)
	}
}
