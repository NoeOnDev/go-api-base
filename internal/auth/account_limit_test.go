package auth_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/NoeOnDev/go-api-base/internal/auth"
	"github.com/NoeOnDev/go-api-base/internal/testutil"
)

// Sensitive actions with a session are limited per user, not per IP: two users behind the same
// IP (an office) do not share the limit.
func TestAccountLimitIsPerUser(t *testing.T) {
	pool := testutil.NewDB(t)
	api := testutil.NewAPI(pool)
	_, alice := testutil.Login(t, pool, "alice@example.com", auth.RoleUser)
	_, bob := testutil.Login(t, pool, "bob@example.com", auth.RoleUser)

	wrong := fmt.Sprintf(`{"current_password":"wrong-password","password":%q}`, newPassword)
	for i := range 5 {
		for name, cookie := range map[string]*http.Cookie{"Alice": alice, "Bob": bob} {
			// All from the same IP (testutil.Do's).
			if w := testutil.Do(t, api, cookie, http.MethodPut, "/auth/password", wrong); w.Code != http.StatusUnprocessableEntity {
				t.Fatalf("%s, attempt %d: status = %d, want 422", name, i+1, w.Code)
			}
		}
	}

	// Alice's sixth one is rejected.
	resp := testutil.Decode[testutil.APIError](t, testutil.Do(t, api, alice, http.MethodPut, "/auth/password", wrong), http.StatusTooManyRequests)
	if resp.Error.Code != "too_many_requests" {
		t.Errorf("unexpected error: %+v", resp.Error)
	}
}
