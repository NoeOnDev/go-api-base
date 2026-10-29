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
	_, alice := testutil.Login(t, pool, "alice@example.com", auth.RoleUser)
	_, bob := testutil.Login(t, pool, "bob@example.com", auth.RoleUser)

	wrong := fmt.Sprintf(`{"current_password":"wrong-password","password":%q}`, newPassword)
	for i := range 5 {
		for name, cookie := range map[string]*http.Cookie{"Alice": alice, "Bob": bob} {
			// Todos desde la misma IP (la de testutil.Do).
			if w := testutil.Do(t, api, cookie, http.MethodPut, "/auth/password", wrong); w.Code != http.StatusUnprocessableEntity {
				t.Fatalf("%s, intento %d: status = %d, se esperaba 422", name, i+1, w.Code)
			}
		}
	}

	// El sexto de Alice se rechaza.
	resp := testutil.Decode[testutil.APIError](t, testutil.Do(t, api, alice, http.MethodPut, "/auth/password", wrong), http.StatusTooManyRequests)
	if resp.Error.Code != "too_many_requests" {
		t.Errorf("error inesperado: %+v", resp.Error)
	}
}
