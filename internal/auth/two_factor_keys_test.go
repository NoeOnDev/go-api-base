package auth_test

import (
	"bytes"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/NoeOnDev/go-api-base/internal/auth"
	"github.com/NoeOnDev/go-api-base/internal/config"
	"github.com/NoeOnDev/go-api-base/internal/secretbox"
	"github.com/NoeOnDev/go-api-base/internal/testutil"
	"github.com/NoeOnDev/go-api-base/internal/totp"
)

// Key A: the tests' one (testutil.EncryptionKey). Key B: the new one, after rotating.
var keyB = bytes.Repeat([]byte{2}, 32)

// serviceWithKeys creates the auth service with a key ring, as the rotate-keys command does.
func serviceWithKeys(t *testing.T, pool *pgxpool.Pool, current []byte, previous ...[]byte) (*auth.Service, *secretbox.Box) {
	t.Helper()
	box, err := secretbox.New(current, previous...)
	if err != nil {
		t.Fatal(err)
	}
	return auth.NewService(pool, auth.Options{Box: box}), box
}

// rotatedAPI is the API after rotating: B is the current key and A stays as a previous one.
func rotatedAPI(pool *pgxpool.Pool) http.Handler {
	return testutil.NewAPI(pool, testutil.WithConfig(func(c *config.Config) {
		c.EncryptionKey = keyB
		c.EncryptionPreviousKeys = [][]byte{testutil.EncryptionKey}
	}))
}

// onlyNewKeyAPI is the API if key A was removed before the rotation finished.
func onlyNewKeyAPI(pool *pgxpool.Pool) http.Handler {
	return testutil.NewAPI(pool, testutil.WithConfig(func(c *config.Config) {
		c.EncryptionKey = keyB
	}))
}

func storedSecret(t *testing.T, pool *pgxpool.Pool, email string) string {
	t.Helper()
	var s string
	if err := pool.QueryRow(t.Context(), "SELECT totp_secret FROM users WHERE email = $1", email).Scan(&s); err != nil {
		t.Fatal(err)
	}
	return s
}

func loginAs(t *testing.T, api http.Handler, email string) *http.Cookie {
	t.Helper()
	body := fmt.Sprintf(`{"email":%q,"password":"test-password"}`, email)
	w := testutil.Do(t, api, nil, http.MethodPost, "/auth/login", body)
	testutil.Decode[map[string]bool](t, w, http.StatusAccepted)
	for _, c := range w.Result().Cookies() {
		if c.Name == auth.ChallengeCookieName {
			return c
		}
	}
	t.Fatal("missing the challenge cookie")
	return nil
}

func TestTwoFactorAfterKeyRotation(t *testing.T) {
	pool := testutil.NewDB(t)
	_, session := testutil.Login(t, pool, "alice@example.com", auth.RoleUser)
	tf := testutil.EnableTwoFactor(t, testutil.NewAPI(pool), session, testutil.Password) // with key A

	boxB, _ := secretbox.New(keyB)
	before := storedSecret(t, pool, "alice@example.com")
	if strings.HasPrefix(before, boxB.KeyID()+":") {
		t.Fatal("the secret should be encrypted with key A")
	}

	api := rotatedAPI(pool)

	// The login with a code works with the previous key…
	if w := challenge(t, api, loginAs(t, api, "alice@example.com"), codeBody(tf.Code(t))); w.Code != http.StatusOK {
		t.Fatalf("login with the previous key: status = %d; body: %s", w.Code, w.Body)
	}
	// …and along the way the secret got encrypted with the new key.
	if after := storedSecret(t, pool, "alice@example.com"); !strings.HasPrefix(after, boxB.KeyID()+":") {
		t.Errorf("the secret was not encrypted again with the new key: %s", after)
	}

	// A recovery code generated with key A still works.
	body := fmt.Sprintf(`{"recovery_code":%q}`, tf.RecoveryCodes[0])
	if w := challenge(t, api, loginAs(t, api, "alice@example.com"), body); w.Code != http.StatusOK {
		t.Errorf("recovery code of the previous key: status = %d", w.Code)
	}
}

func TestTwoFactorWithMissingKey(t *testing.T) {
	pool := testutil.NewDB(t)
	_, session := testutil.Login(t, pool, "alice@example.com", auth.RoleUser)
	tf := testutil.EnableTwoFactor(t, testutil.NewAPI(pool), session, testutil.Password) // with key A

	// Key A was removed without rotating: the user gets a clear error, not a 500.
	api := onlyNewKeyAPI(pool)
	resp := testutil.Decode[testutil.APIError](t,
		challenge(t, api, loginAs(t, api, "alice@example.com"), codeBody(tf.Code(t))), http.StatusServiceUnavailable)
	if resp.Error.Code != "two_factor_unavailable" || strings.Contains(resp.Error.Message, "key") {
		t.Errorf("unexpected error (it must not expose internal details): %+v", resp.Error)
	}

	// The recovery codes of key A do not work without it either.
	body := fmt.Sprintf(`{"recovery_code":%q}`, tf.RecoveryCodes[0])
	testutil.Decode[testutil.APIError](t, challenge(t, api, loginAs(t, api, "alice@example.com"), body), http.StatusUnprocessableEntity)
}

func TestTwoFactorDataIsBoundToUser(t *testing.T) {
	pool := testutil.NewDB(t)
	setup := testutil.NewAPI(pool)
	_, alice := testutil.Login(t, pool, "alice@example.com", auth.RoleUser)
	bob, bobSession := testutil.Login(t, pool, "bob@example.com", auth.RoleUser)
	tfAlice := testutil.EnableTwoFactor(t, setup, alice, testutil.Password)
	testutil.EnableTwoFactor(t, setup, bobSession, testutil.Password)

	// Someone with write access to the database copies Alice's secret to Bob and hands him Alice's
	// recovery codes.
	for _, q := range []string{
		"UPDATE users SET totp_secret = (SELECT totp_secret FROM users WHERE email = 'alice@example.com') WHERE email = 'bob@example.com'",
		"DELETE FROM user_recovery_codes WHERE user_id = $1",
		"UPDATE user_recovery_codes SET user_id = $1",
	} {
		args := []any{bob.ID}
		if !strings.Contains(q, "$1") {
			args = nil
		}
		if _, err := pool.Exec(t.Context(), q, args...); err != nil {
			t.Fatal(err)
		}
	}

	// With Alice's app they cannot sign in as Bob: the secret does not decrypt outside its user.
	api := testutil.NewAPI(pool)
	resp := testutil.Decode[testutil.APIError](t,
		challenge(t, api, loginAs(t, api, "bob@example.com"), codeBody(tfAlice.Code(t))), http.StatusServiceUnavailable)
	if resp.Error.Code != "two_factor_unavailable" {
		t.Errorf("unexpected error: %+v", resp.Error)
	}

	// Alice's recovery codes do not work for Bob either.
	body := fmt.Sprintf(`{"recovery_code":%q}`, tfAlice.RecoveryCodes[0])
	testutil.Decode[testutil.APIError](t, challenge(t, api, loginAs(t, api, "bob@example.com"), body), http.StatusUnprocessableEntity)
}

// A pending secret encrypted with a previous key is enabled anyway. Enable does not encrypt it
// again (the login and rotate-keys do): if it did, the secret would no longer match the one that
// was verified and EnableTOTP would not enable it.
func TestEnableTwoFactorAfterKeyRotation(t *testing.T) {
	pool := testutil.NewDB(t)
	_, session := testutil.Login(t, pool, "alice@example.com", auth.RoleUser)
	setup := testutil.Decode[setupResponse](t, testutil.Do(t, testutil.NewAPI(pool), session, http.MethodPost, "/auth/two-factor/setup", ""), http.StatusOK) // with key A

	api := rotatedAPI(pool)
	code, err := totp.Code(setup.Secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if w := testutil.Do(t, api, session, http.MethodPost, "/auth/two-factor/enable", enableBody(code, testutil.Password)); w.Code != http.StatusOK {
		t.Fatalf("enable with a secret of the previous key: status = %d; body: %s", w.Code, w.Body)
	}

	// The login with a code works, and then the secret moves to the new key.
	tf := testutil.TwoFactor{Secret: setup.Secret}
	if w := challenge(t, api, loginAs(t, api, "alice@example.com"), codeBody(tf.Code(t))); w.Code != http.StatusOK {
		t.Fatalf("login after enabling: status = %d; body: %s", w.Code, w.Body)
	}
	boxB, _ := secretbox.New(keyB)
	if after := storedSecret(t, pool, "alice@example.com"); !strings.HasPrefix(after, boxB.KeyID()+":") {
		t.Errorf("the secret was not encrypted again with the new key on login: %s", after)
	}
}

func TestRotateKeys(t *testing.T) {
	pool := testutil.NewDB(t)
	apiA := testutil.NewAPI(pool) // key A

	// Two users with two-factor authentication on and one halfway through setting it up, all with key A.
	_, alice := testutil.Login(t, pool, "alice@example.com", auth.RoleUser)
	_, bob := testutil.Login(t, pool, "bob@example.com", auth.RoleUser)
	_, carol := testutil.Login(t, pool, "carol@example.com", auth.RoleUser)
	testutil.EnableTwoFactor(t, apiA, alice, testutil.Password)
	testutil.EnableTwoFactor(t, apiA, bob, testutil.Password)
	testutil.Decode[setupResponse](t, testutil.Do(t, apiA, carol, http.MethodPost, "/auth/two-factor/setup", ""), http.StatusOK)

	// Rotation: B is the current key and A stays as a previous one.
	service, boxB := serviceWithKeys(t, pool, keyB, testutil.EncryptionKey)
	boxA, _ := secretbox.New(testutil.EncryptionKey)

	report, err := service.RotateKeys(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if report.CurrentKeyID != boxB.KeyID() || report.Reencrypted != 3 || report.AlreadyCurrent != 0 {
		t.Errorf("unexpected report: %+v", report)
	}
	// The recovery codes of A cannot be computed again: the report says so.
	if len(report.OldRecoveryCodes) != 1 || report.OldRecoveryCodes[0] != (auth.KeyUsage{KeyID: boxA.KeyID(), Users: 2}) {
		t.Errorf("unexpected old codes: %+v", report.OldRecoveryCodes)
	}
	if report.Done() || len(report.RemovableKeys) != 0 {
		t.Errorf("key A is still in use and should not be removable: %+v", report)
	}

	// A second pass encrypts nothing again.
	again, err := service.RotateKeys(t.Context())
	if err != nil || again.Reencrypted != 0 || again.AlreadyCurrent != 3 {
		t.Errorf("segunda pasada inesperada: %+v (%v)", again, err)
	}

	// When both users regenerate their codes with the new key, the rotation finishes.
	apiB := testutil.NewAPI(pool, testutil.WithConfig(func(c *config.Config) {
		c.EncryptionKey = keyB
		c.EncryptionPreviousKeys = [][]byte{testutil.EncryptionKey}
	}))
	for _, cookie := range []*http.Cookie{alice, bob} {
		testutil.Decode[recoveryCodes](t,
			testutil.Do(t, apiB, cookie, http.MethodPost, "/auth/two-factor/recovery-codes", `{"password":"test-password"}`), http.StatusOK)
	}

	done, err := service.RotateKeys(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !done.Done() || len(done.RemovableKeys) != 1 || done.RemovableKeys[0] != boxA.KeyID() {
		t.Errorf("the rotation should be complete and key A removable: %+v", done)
	}
}

func TestRotateKeysReportsUndecryptable(t *testing.T) {
	pool := testutil.NewDB(t)

	// A user with the secret encrypted with a key C that is not in the key ring.
	keyC := bytes.Repeat([]byte{3}, 32)
	apiC := testutil.NewAPI(pool, testutil.WithConfig(func(c *config.Config) { c.EncryptionKey = keyC }))
	user, cookie := testutil.Login(t, pool, "alice@example.com", auth.RoleUser)
	testutil.EnableTwoFactor(t, apiC, cookie, testutil.Password)

	service, _ := serviceWithKeys(t, pool, keyB, testutil.EncryptionKey)
	report, err := service.RotateKeys(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	boxC, _ := secretbox.New(keyC)
	if len(report.Undecryptable) != 1 || report.Undecryptable[0].UserID != user.ID || report.Undecryptable[0].KeyID != boxC.KeyID() {
		t.Errorf("want an undecrypted secret of user %d: %+v", user.ID, report.Undecryptable)
	}
	if report.Done() {
		t.Error("the rotation should not be done")
	}
}
