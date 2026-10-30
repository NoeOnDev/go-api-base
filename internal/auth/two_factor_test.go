package auth_test

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/NoeOnDev/go-api-base/internal/auth"
	"github.com/NoeOnDev/go-api-base/internal/store"
	"github.com/NoeOnDev/go-api-base/internal/testutil"
	"github.com/NoeOnDev/go-api-base/internal/totp"
)

type setupResponse struct {
	Secret     string `json:"secret"`
	OTPAuthURL string `json:"otpauth_url"`
}

type recoveryCodes struct {
	RecoveryCodes []string `json:"recovery_codes"`
}

func codeBody(code string) string { return fmt.Sprintf(`{"code":%q}`, code) }

func enableBody(code, password string) string {
	return fmt.Sprintf(`{"code":%q,"password":%q}`, code, password)
}

func TestEnableTwoFactor(t *testing.T) {
	pool := testutil.NewDB(t)
	api := testutil.NewAPI(pool)
	user, cookie := testutil.Login(t, pool, "alice@example.com", auth.RoleUser)
	other := newSession(t, pool, user.ID)

	// Setup returns the secret and the QR URL, with the app name and the email.
	setup := testutil.Decode[setupResponse](t, testutil.Do(t, api, cookie, http.MethodPost, "/auth/two-factor/setup", ""), http.StatusOK)
	u, err := url.Parse(setup.OTPAuthURL)
	if err != nil || u.Scheme != "otpauth" || u.Query().Get("secret") != setup.Secret || u.Query().Get("issuer") != "Test App" ||
		!strings.Contains(u.Path, "alice@example.com") {
		t.Fatalf("unexpected otpauth_url: %s", setup.OTPAuthURL)
	}

	// In the database the secret is encrypted, not in base32.
	var stored string
	if err := pool.QueryRow(t.Context(), "SELECT totp_secret FROM users WHERE id = $1", user.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored == setup.Secret || strings.Contains(stored, setup.Secret) {
		t.Error("the secret is stored unencrypted")
	}

	// Until it is confirmed, two-factor authentication is not on.
	me := testutil.Decode[auth.User](t, testutil.Do(t, api, cookie, http.MethodGet, "/auth/me", ""), http.StatusOK)
	if me.TwoFactorEnabled {
		t.Fatal("two-factor authentication should not be on before it is confirmed")
	}

	// It asks for the password: with a stolen session, someone would enable two-factor
	// authentication with their own app and, as the other sessions close, lock the owner out.
	// Careful: the route has a per-user limit (5 in a row); this test makes 4 requests to it.
	code, _ := totp.Code(setup.Secret, time.Now())
	for name, tt := range map[string]struct{ body, field string }{
		"without a password": {codeBody(code), "password"},
		"wrong password":     {enableBody(code, "wrong-password"), "password"},
		"wrong code":         {enableBody("000000", "test-password"), "code"},
	} {
		resp := testutil.Decode[testutil.APIError](t,
			testutil.Do(t, api, cookie, http.MethodPost, "/auth/two-factor/enable", tt.body), http.StatusUnprocessableEntity)
		if resp.Error.Fields[tt.field] == "" {
			t.Errorf("%s: want an error on %s: %+v", name, tt.field, resp.Error)
		}
	}
	me = testutil.Decode[auth.User](t, testutil.Do(t, api, cookie, http.MethodGet, "/auth/me", ""), http.StatusOK)
	if me.TwoFactorEnabled {
		t.Fatal("two-factor authentication should not turn on without the right password and code")
	}

	codes := testutil.Decode[recoveryCodes](t,
		testutil.Do(t, api, cookie, http.MethodPost, "/auth/two-factor/enable", enableBody(code, "test-password")), http.StatusOK)

	if len(codes.RecoveryCodes) != 10 {
		t.Fatalf("want 10 recovery codes: %v", codes.RecoveryCodes)
	}
	for _, c := range codes.RecoveryCodes {
		if len(c) != 11 || c[5] != '-' {
			t.Errorf("code with an unexpected format: %q", c)
		}
	}

	// The codes are not stored in plain text.
	var plain int
	if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM user_recovery_codes WHERE code_hash = convert_to($1, 'UTF8')",
		codes.RecoveryCodes[0]).Scan(&plain); err != nil || plain != 0 {
		t.Errorf("recovery code in plain text: %d (%v)", plain, err)
	}

	me = testutil.Decode[auth.User](t, testutil.Do(t, api, cookie, http.MethodGet, "/auth/me", ""), http.StatusOK)
	if !me.TwoFactorEnabled {
		t.Error("two-factor authentication should be on")
	}

	// Enabling two-factor authentication closes the other sessions.
	if w := testutil.Do(t, api, other, http.MethodGet, "/auth/me", ""); w.Code != http.StatusUnauthorized {
		t.Errorf("the other session is still active: status = %d", w.Code)
	}

	// Once on, setup and enable answer 409.
	testutil.Decode[testutil.APIError](t, testutil.Do(t, api, cookie, http.MethodPost, "/auth/two-factor/setup", ""), http.StatusConflict)
}

func TestEnableTwoFactorRequiresSetup(t *testing.T) {
	pool := testutil.NewDB(t)
	api := testutil.NewAPI(pool)
	_, cookie := testutil.Login(t, pool, "alice@example.com", auth.RoleUser)

	testutil.Decode[testutil.APIError](t,
		testutil.Do(t, api, cookie, http.MethodPost, "/auth/two-factor/enable", enableBody("123456", "test-password")), http.StatusConflict)
	testutil.Decode[testutil.APIError](t,
		testutil.Do(t, api, nil, http.MethodPost, "/auth/two-factor/setup", ""), http.StatusUnauthorized)
}

func disableBody(password, field, code string) string {
	return fmt.Sprintf(`{"password":%q,%q:%q}`, password, field, code)
}

func TestDisableTwoFactor(t *testing.T) {
	pool := testutil.NewDB(t)
	api := testutil.NewAPI(pool)
	_, cookie := testutil.Login(t, pool, "alice@example.com", auth.RoleUser)
	tf := testutil.EnableTwoFactor(t, api, cookie, testutil.Password)

	// A wrong code leaves it on.
	w := testutil.Do(t, api, cookie, http.MethodPost, "/auth/two-factor/disable", disableBody(testutil.Password, "code", "000000"))
	if resp := testutil.Decode[testutil.APIError](t, w, http.StatusUnprocessableEntity); resp.Error.Fields["code"] == "" {
		t.Errorf("want an error on code: %+v", resp.Error)
	}

	w = testutil.Do(t, api, cookie, http.MethodPost, "/auth/two-factor/disable", disableBody(testutil.Password, "code", tf.Code(t)))
	if w.Code != http.StatusNoContent {
		t.Fatalf("disable: status = %d; body: %s", w.Code, w.Body)
	}

	me := testutil.Decode[auth.User](t, testutil.Do(t, api, cookie, http.MethodGet, "/auth/me", ""), http.StatusOK)
	if me.TwoFactorEnabled {
		t.Error("two-factor authentication should be off")
	}
	var codes int
	if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM user_recovery_codes").Scan(&codes); err != nil || codes != 0 {
		t.Errorf("%d recovery codes left (%v)", codes, err)
	}

	// Already off: 409.
	w = testutil.Do(t, api, cookie, http.MethodPost, "/auth/two-factor/disable", disableBody(testutil.Password, "code", "123456"))
	if resp := testutil.Decode[testutil.APIError](t, w, http.StatusConflict); resp.Error.Code != "two_factor_not_enabled" {
		t.Errorf("code = %q, want two_factor_not_enabled", resp.Error.Code)
	}
}

// It asks for the password and exactly one second factor. The account actions allow 5 in a row
// per user, and enabling it already spent one: hence a test of its own.
func TestDisableTwoFactorValidation(t *testing.T) {
	pool := testutil.NewDB(t)
	api := testutil.NewAPI(pool)
	_, cookie := testutil.Login(t, pool, "alice@example.com", auth.RoleUser)
	tf := testutil.EnableTwoFactor(t, api, cookie, testutil.Password)

	for name, tt := range map[string]struct{ body, field string }{
		"wrong password":      {disableBody("wrong-password", "code", tf.Code(t)), "password"},
		"without a code":      {fmt.Sprintf(`{"password":%q}`, testutil.Password), "code"},
		"both kinds of code":  {fmt.Sprintf(`{"password":%q,"code":"000000","recovery_code":"AAAAA-BBBBB"}`, testutil.Password), "code"},
		"wrong recovery code": {disableBody(testutil.Password, "recovery_code", "AAAAA-BBBBB"), "recovery_code"},
	} {
		w := testutil.Do(t, api, cookie, http.MethodPost, "/auth/two-factor/disable", tt.body)
		resp := testutil.Decode[testutil.APIError](t, w, http.StatusUnprocessableEntity)
		if resp.Error.Fields[tt.field] == "" {
			t.Errorf("%s: want an error on %s: %+v", name, tt.field, resp.Error)
		}
	}

	if me := testutil.Decode[auth.User](t, testutil.Do(t, api, cookie, http.MethodGet, "/auth/me", ""), http.StatusOK); !me.TwoFactorEnabled {
		t.Error("two-factor authentication should stay on")
	}
}

// Whoever lost their phone turns it off with a recovery code.
func TestDisableTwoFactorWithRecoveryCode(t *testing.T) {
	pool := testutil.NewDB(t)
	api := testutil.NewAPI(pool)
	_, cookie := testutil.Login(t, pool, "alice@example.com", auth.RoleUser)
	tf := testutil.EnableTwoFactor(t, api, cookie, testutil.Password)

	w := testutil.Do(t, api, cookie, http.MethodPost, "/auth/two-factor/disable", disableBody(testutil.Password, "recovery_code", tf.RecoveryCodes[0]))
	if w.Code != http.StatusNoContent {
		t.Fatalf("disable: status = %d; body: %s", w.Code, w.Body)
	}
	if me := testutil.Decode[auth.User](t, testutil.Do(t, api, cookie, http.MethodGet, "/auth/me", ""), http.StatusOK); me.TwoFactorEnabled {
		t.Error("two-factor authentication should be off")
	}
}

func TestRegenerateRecoveryCodes(t *testing.T) {
	pool := testutil.NewDB(t)
	api := testutil.NewAPI(pool)
	_, cookie := testutil.Login(t, pool, "alice@example.com", auth.RoleUser)

	// Without two-factor authentication there are no codes to regenerate.
	testutil.Decode[testutil.APIError](t,
		testutil.Do(t, api, cookie, http.MethodPost, "/auth/two-factor/recovery-codes", `{"password":"test-password"}`), http.StatusConflict)

	first := testutil.EnableTwoFactor(t, api, cookie, testutil.Password)
	second := testutil.Decode[recoveryCodes](t,
		testutil.Do(t, api, cookie, http.MethodPost, "/auth/two-factor/recovery-codes", `{"password":"test-password"}`), http.StatusOK)

	if len(second.RecoveryCodes) != 10 || second.RecoveryCodes[0] == first.RecoveryCodes[0] {
		t.Errorf("unexpected new codes: %v", second.RecoveryCodes)
	}
	var count int
	if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM user_recovery_codes").Scan(&count); err != nil || count != 10 {
		t.Errorf("only the 10 new codes should be left, there are %d (%v)", count, err)
	}
}

// EnableTOTP only enables the secret that was verified: if meanwhile another request asked for a
// new QR code (setup), it does not enable that one, which the user never scanned and whose secret
// went to whoever asked for it.
func TestEnableTOTPRequiresVerifiedSecret(t *testing.T) {
	pool := testutil.NewDB(t)
	api := testutil.NewAPI(pool)
	user, cookie := testutil.Login(t, pool, "alice@example.com", auth.RoleUser)
	testutil.Decode[setupResponse](t, testutil.Do(t, api, cookie, http.MethodPost, "/auth/two-factor/setup", ""), http.StatusOK)

	q := store.New(pool)
	verified, err := q.GetUserByID(t.Context(), user.ID) // the secret enable verified the code with
	if err != nil {
		t.Fatal(err)
	}
	// Before enabling it, another request asks for a new QR code.
	testutil.Decode[setupResponse](t, testutil.Do(t, api, cookie, http.MethodPost, "/auth/two-factor/setup", ""), http.StatusOK)

	step := int64(1)
	rows, err := q.EnableTOTP(t.Context(), store.EnableTOTPParams{ID: user.ID, TOTPSecret: verified.TOTPSecret, TOTPLastStep: &step})
	if err != nil || rows != 0 {
		t.Fatalf("EnableTOTP with a replaced secret: rows = %d, error = %v; want 0", rows, err)
	}
	if me := testutil.Decode[auth.User](t, testutil.Do(t, api, cookie, http.MethodGet, "/auth/me", ""), http.StatusOK); me.TwoFactorEnabled {
		t.Error("two-factor authentication should not have turned on")
	}
}

// If a step of enabling two-factor authentication fails, it stays off: without recovery codes
// and with the other sessions open. The same code works on retry, because its step was not saved.
func TestEnableTwoFactorIsAtomic(t *testing.T) {
	pool := testutil.NewDB(t)
	api := testutil.NewAPI(pool)
	u, cookie := testutil.Login(t, pool, "alice@example.com", auth.RoleUser)
	other := newSession(t, pool, u.ID) // another session, which would be closed

	setup := testutil.Decode[struct {
		Secret string `json:"secret"`
	}](t, testutil.Do(t, api, cookie, http.MethodPost, "/auth/two-factor/setup", ""), http.StatusOK)
	code, err := totp.Code(setup.Secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}

	exec := func(sql string) error { _, err := pool.Exec(context.Background(), sql); return err }
	failSessionDeletes(t, exec)

	// Closing the other sessions fails, the last step: everything is undone.
	testutil.Decode[testutil.APIError](t, testutil.Do(t, api, cookie, http.MethodPost, "/auth/two-factor/enable", enableBody(code, testutil.Password)), http.StatusInternalServerError)

	if me := testutil.Decode[auth.User](t, testutil.Do(t, api, cookie, http.MethodGet, "/auth/me", ""), http.StatusOK); me.TwoFactorEnabled {
		t.Error("two-factor authentication should not have turned on")
	}
	if n := countRows(t, pool, "user_recovery_codes", u.ID); n != 0 {
		t.Errorf("%d recovery codes left, want none", n)
	}
	if w := testutil.Do(t, api, other, http.MethodGet, "/auth/me", ""); w.Code != http.StatusOK {
		t.Errorf("the other session should stay open: status = %d", w.Code)
	}

	// Without the failure, the same code works.
	if err := exec("DROP TRIGGER test_fail_session_delete ON sessions"); err != nil {
		t.Fatal(err)
	}
	if w := testutil.Do(t, api, cookie, http.MethodPost, "/auth/two-factor/enable", enableBody(code, testutil.Password)); w.Code != http.StatusOK {
		t.Fatalf("the retry should enable two-factor authentication: status = %d; body: %s", w.Code, w.Body)
	}
}

// The same when disabling it: it stays on, with its codes and the other sessions.
func TestDisableTwoFactorIsAtomic(t *testing.T) {
	pool := testutil.NewDB(t)
	api := testutil.NewAPI(pool)
	u, cookie := testutil.Login(t, pool, "alice@example.com", auth.RoleUser)
	tf := testutil.EnableTwoFactor(t, api, cookie, testutil.Password)
	other := newSession(t, pool, u.ID) // after enabling it, which closes the others

	exec := func(sql string) error { _, err := pool.Exec(context.Background(), sql); return err }
	failSessionDeletes(t, exec)

	body := disableBody(testutil.Password, "code", tf.Code(t))
	testutil.Decode[testutil.APIError](t, testutil.Do(t, api, cookie, http.MethodPost, "/auth/two-factor/disable", body), http.StatusInternalServerError)

	if me := testutil.Decode[auth.User](t, testutil.Do(t, api, cookie, http.MethodGet, "/auth/me", ""), http.StatusOK); !me.TwoFactorEnabled {
		t.Error("two-factor authentication should stay on")
	}
	if n := countRows(t, pool, "user_recovery_codes", u.ID); n == 0 {
		t.Error("the recovery codes were deleted")
	}
	if w := testutil.Do(t, api, other, http.MethodGet, "/auth/me", ""); w.Code != http.StatusOK {
		t.Errorf("the other session should stay open: status = %d", w.Code)
	}
}
