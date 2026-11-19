package auth_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/NoeOnDev/go-api-base/internal/auth"
	"github.com/NoeOnDev/go-api-base/internal/config"
	"github.com/NoeOnDev/go-api-base/internal/testutil"
)

func requireAdminTwoFactor(c *config.Config) { c.RequireAdminTwoFactor = true }

// With REQUIRE_ADMIN_TWO_FACTOR, an admin acts as one only after turning on two-factor
// authentication: until then the admin routes answer 403 and the admin-only data is hidden.
func TestRequireAdminTwoFactor(t *testing.T) {
	pool := testutil.NewDB(t)
	api := testutil.NewAPI(pool, testutil.WithConfig(requireAdminTwoFactor))
	routes := protected(pool, requireAdminTwoFactor)
	_, admin := testutil.Login(t, pool, "admin@example.com", auth.RoleAdmin)

	// GET /admin/users goes through the whole API: it also checks that the option reaches auth.
	for _, tt := range []struct {
		handler http.Handler
		path    string
	}{{api, "/admin/users"}, {routes, "/admin"}} {
		w := testutil.Do(t, tt.handler, admin, http.MethodGet, tt.path, "")
		if resp := testutil.Decode[testutil.APIError](t, w, http.StatusForbidden); resp.Error.Code != "admin_two_factor_required" {
			t.Errorf("%s: code = %q, want admin_two_factor_required", tt.path, resp.Error.Code)
		}
	}
	// A route with OptionalUser treats them as any user: it does not show what only admins see.
	if who := testutil.Do(t, routes, admin, http.MethodGet, "/optional", "").Body.String(); who != "user" {
		t.Errorf("without two-factor authentication, OptionalUser sees %q, want user", who)
	}

	// The account routes only ask for a session: the admin can turn it on.
	if me := testutil.Decode[auth.User](t, testutil.Do(t, api, admin, http.MethodGet, "/auth/me", ""), http.StatusOK); me.Role != auth.RoleAdmin {
		t.Errorf("role = %q, want admin", me.Role)
	}
	testutil.EnableTwoFactor(t, api, admin, testutil.Password)

	if w := testutil.Do(t, api, admin, http.MethodGet, "/admin/users", ""); w.Code != http.StatusOK {
		t.Errorf("/admin/users with two-factor authentication: status = %d", w.Code)
	}
	if w := testutil.Do(t, routes, admin, http.MethodGet, "/admin", ""); w.Code != http.StatusNoContent {
		t.Errorf("/admin with two-factor authentication: status = %d", w.Code)
	}
	if who := testutil.Do(t, routes, admin, http.MethodGet, "/optional", "").Body.String(); who != "admin" {
		t.Errorf("with two-factor authentication, OptionalUser sees %q, want admin", who)
	}
}

// The only admin cannot delete their account, even before turning on two-factor authentication.
func TestRequireAdminTwoFactorKeepsLastAdmin(t *testing.T) {
	pool := testutil.NewDB(t)
	api := testutil.NewAPI(pool, testutil.WithConfig(requireAdminTwoFactor))
	_, admin := testutil.Login(t, pool, "admin@example.com", auth.RoleAdmin)

	body := fmt.Sprintf(`{"password":%q}`, testutil.Password)
	w := testutil.Do(t, api, admin, http.MethodDelete, "/auth/me", body)
	if resp := testutil.Decode[testutil.APIError](t, w, http.StatusConflict); resp.Error.Code != "last_admin" {
		t.Errorf("code = %q, want last_admin", resp.Error.Code)
	}
}
