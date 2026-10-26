package auth_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/NoeOnDev/go-api-base/internal/auth"
	"github.com/NoeOnDev/go-api-base/internal/mail"
	"github.com/NoeOnDev/go-api-base/internal/testutil"
	"github.com/NoeOnDev/go-api-base/internal/totp"
)

// failSessionDeletes hace que la base rechace borrar sesiones, para simular una falla a mitad
// de una operación. Se deshace al terminar el test.
func failSessionDeletes(t *testing.T, exec func(sql string) error) {
	t.Helper()
	t.Cleanup(func() {
		_ = exec("DROP TRIGGER IF EXISTS test_fail_session_delete ON sessions")
		_ = exec("DROP FUNCTION IF EXISTS test_fail_session_delete()")
	})
	for _, sql := range []string{
		`CREATE FUNCTION test_fail_session_delete() RETURNS trigger LANGUAGE plpgsql
		 AS $$ BEGIN RAISE EXCEPTION 'falla de prueba'; END $$`,
		`CREATE TRIGGER test_fail_session_delete BEFORE DELETE ON sessions
		 FOR EACH ROW EXECUTE FUNCTION test_fail_session_delete()`,
	} {
		if err := exec(sql); err != nil {
			t.Fatal(err)
		}
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

// Lo mismo al cambiar la contraseña desde el perfil.
func TestChangePasswordIsAtomic(t *testing.T) {
	pool := testutil.NewDB(t)
	api := testutil.NewAPI(pool)
	_, cookie := testutil.Login(t, pool, "ana@ejemplo.com", auth.RoleUser)
	testutil.Do(t, api, nil, http.MethodPost, "/auth/login", loginBody) // otra sesión, que se cerraría

	exec := func(sql string) error { _, err := pool.Exec(context.Background(), sql); return err }
	failSessionDeletes(t, exec)

	body := fmt.Sprintf(`{"current_password":"contraseña-de-prueba","password":%q}`, newPassword)
	testutil.Decode[testutil.APIError](t, testutil.Do(t, api, cookie, http.MethodPut, "/auth/password", body), http.StatusInternalServerError)

	if _, err := auth.NewService(pool, auth.Options{}).Authenticate(t.Context(), "ana@ejemplo.com", "contraseña-de-prueba"); err != nil {
		t.Errorf("la contraseña no debería haber cambiado: %v", err)
	}
}

// Si un paso de activar el 2FA falla, sigue inactivo: sin códigos de recuperación y con las demás
// sesiones abiertas. El mismo código sirve al reintentar, porque su paso no quedó guardado.
func TestEnableTwoFactorIsAtomic(t *testing.T) {
	pool := testutil.NewDB(t)
	api := testutil.NewAPI(pool)
	u, cookie := testutil.Login(t, pool, "ana@ejemplo.com", auth.RoleUser)
	other := newSession(t, pool, u.ID) // otra sesión, que se cerraría

	setup := testutil.Decode[struct {
		Secret string `json:"secret"`
	}](t, testutil.Do(t, api, cookie, http.MethodPost, "/auth/two-factor/setup", ""), http.StatusOK)
	code, err := totp.Code(setup.Secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}

	exec := func(sql string) error { _, err := pool.Exec(context.Background(), sql); return err }
	failSessionDeletes(t, exec)

	// Falla el cierre de las demás sesiones, el último paso: se deshace todo.
	testutil.Decode[testutil.APIError](t, testutil.Do(t, api, cookie, http.MethodPost, "/auth/two-factor/enable", enableBody(code, testutil.Password)), http.StatusInternalServerError)

	if me := testutil.Decode[auth.User](t, testutil.Do(t, api, cookie, http.MethodGet, "/auth/me", ""), http.StatusOK); me.TwoFactorEnabled {
		t.Error("el 2FA no debería haber quedado activo")
	}
	if n := countRows(t, pool, "user_recovery_codes", u.ID); n != 0 {
		t.Errorf("quedaron %d códigos de recuperación, se esperaba ninguno", n)
	}
	if w := testutil.Do(t, api, other, http.MethodGet, "/auth/me", ""); w.Code != http.StatusOK {
		t.Errorf("la otra sesión debería seguir abierta: status = %d", w.Code)
	}

	// Sin la falla, el mismo código funciona.
	if err := exec("DROP TRIGGER test_fail_session_delete ON sessions"); err != nil {
		t.Fatal(err)
	}
	if w := testutil.Do(t, api, cookie, http.MethodPost, "/auth/two-factor/enable", enableBody(code, testutil.Password)); w.Code != http.StatusOK {
		t.Fatalf("el reintento debería activar el 2FA: status = %d; cuerpo: %s", w.Code, w.Body)
	}
}

// Lo mismo al desactivarlo: sigue activo, con sus códigos y las demás sesiones.
func TestDisableTwoFactorIsAtomic(t *testing.T) {
	pool := testutil.NewDB(t)
	api := testutil.NewAPI(pool)
	u, cookie := testutil.Login(t, pool, "ana@ejemplo.com", auth.RoleUser)
	testutil.EnableTwoFactor(t, api, cookie, testutil.Password)
	other := newSession(t, pool, u.ID) // después de activarlo, que cierra las demás

	exec := func(sql string) error { _, err := pool.Exec(context.Background(), sql); return err }
	failSessionDeletes(t, exec)

	body := fmt.Sprintf(`{"password":%q}`, testutil.Password)
	testutil.Decode[testutil.APIError](t, testutil.Do(t, api, cookie, http.MethodPost, "/auth/two-factor/disable", body), http.StatusInternalServerError)

	if me := testutil.Decode[auth.User](t, testutil.Do(t, api, cookie, http.MethodGet, "/auth/me", ""), http.StatusOK); !me.TwoFactorEnabled {
		t.Error("el 2FA debería seguir activo")
	}
	if n := countRows(t, pool, "user_recovery_codes", u.ID); n == 0 {
		t.Error("se borraron los códigos de recuperación")
	}
	if w := testutil.Do(t, api, other, http.MethodGet, "/auth/me", ""); w.Code != http.StatusOK {
		t.Errorf("la otra sesión debería seguir abierta: status = %d", w.Code)
	}
}

// Varios pedidos de enlace simultáneos para el mismo usuario dejan un solo enlace válido.
// (Sin el bloqueo de la fila del usuario, este test podía fallar o no según coincidieran en el
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

type failingMailer struct{}

func (failingMailer) Send(context.Context, mail.Message) error { return errors.New("cola caída") }

// Si el correo de verificación no se puede encolar, la cuenta igual quedó creada: se responde
// 201, y el usuario puede pedir otro enlace.
func TestRegisterSucceedsWhenVerificationEmailFails(t *testing.T) {
	pool := testutil.NewDB(t)
	api := testutil.NewAPI(pool, testutil.WithMailer(failingMailer{}), testutil.WithConfig(requireVerification))

	w := testutil.Do(t, api, nil, http.MethodPost, "/auth/register", registerBody)
	u := testutil.Decode[auth.User](t, w, http.StatusCreated)
	if u.EmailVerified() {
		t.Error("el email no debería estar verificado")
	}
	sessionCookie(t, w) // y quedó la sesión
}
