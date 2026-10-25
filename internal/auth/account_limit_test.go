package auth_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/NoeOnDev/go-api-base/internal/auth"
	"github.com/NoeOnDev/go-api-base/internal/testutil"
)

// Las acciones sensibles con sesión se limitan por usuario, no por IP: dos usuarios detrás de
// la misma IP (una oficina) no comparten el límite.
func TestAccountLimitIsPerUser(t *testing.T) {
	pool := testutil.NewDB(t)
	api := testutil.NewAPI(pool)
	_, ana := testutil.Login(t, pool, "ana@ejemplo.com", auth.RoleUser)
	_, luis := testutil.Login(t, pool, "luis@ejemplo.com", auth.RoleUser)

	wrong := fmt.Sprintf(`{"current_password":"incorrecta","password":%q}`, newPassword)
	for i := range 5 {
		for name, cookie := range map[string]*http.Cookie{"Ana": ana, "Luis": luis} {
			// Todos desde la misma IP (la de testutil.Do).
			if w := testutil.Do(t, api, cookie, http.MethodPut, "/auth/password", wrong); w.Code != http.StatusUnprocessableEntity {
				t.Fatalf("%s, intento %d: status = %d, se esperaba 422", name, i+1, w.Code)
			}
		}
	}

	// El sexto de Ana se rechaza.
	resp := testutil.Decode[testutil.APIError](t, testutil.Do(t, api, ana, http.MethodPut, "/auth/password", wrong), http.StatusTooManyRequests)
	if resp.Error.Code != "too_many_requests" {
		t.Errorf("error inesperado: %+v", resp.Error)
	}
}

// El segundo paso del 2FA tiene su propio límite: no gasta los intentos del login. Con el límite
// compartido de antes (5 por IP), este test se cortaba en el tercer login.
func TestTwoFactorChallengeHasItsOwnLimit(t *testing.T) {
	pool := testutil.NewDB(t)
	api := testutil.NewAPI(pool)
	_, session := testutil.Login(t, pool, "ana@ejemplo.com", auth.RoleUser)
	testutil.EnableTwoFactor(t, api, session)

	// 10 logins con su segundo paso (con un código incorrecto), todos desde la misma IP.
	for i := range 10 {
		pending := loginWithTwoFactor(t, api) // falla el test si el login no responde 202
		if w := challenge(t, api, pending, codeBody("000000")); w.Code != http.StatusUnprocessableEntity {
			t.Fatalf("intento %d del 2FA: status = %d, se esperaba 422", i+1, w.Code)
		}
	}
}
