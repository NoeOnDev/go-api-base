package auth_test

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/NoeOnDev/go-api-base/internal/auth"
	"github.com/NoeOnDev/go-api-base/internal/config"
	"github.com/NoeOnDev/go-api-base/internal/testutil"
)

// me returns the account of the cookie, as GET /auth/me answers it.
func me(t *testing.T, api http.Handler, cookie *http.Cookie) auth.User {
	t.Helper()
	return testutil.Decode[auth.User](t, testutil.Do(t, api, cookie, http.MethodGet, "/auth/me", ""), http.StatusOK)
}

// A role gives its users what its permissions say: the routes that ask for one let them in, and a
// handler can ask with Can. An administrator has every permission, and the user role none.
func TestPermissions(t *testing.T) {
	pool := testutil.NewDB(t)
	api := authAPI(pool)
	testutil.CreateRole(t, pool, "auditor", permView)
	testutil.CreateRole(t, pool, "analyst", permView, permExport)
	_, admin := testutil.Login(t, pool, "admin@example.com", auth.RoleAdmin)
	_, auditor := testutil.Login(t, pool, "auditor@example.com", "auditor")
	_, analyst := testutil.Login(t, pool, "analyst@example.com", "analyst")
	_, user := testutil.Login(t, pool, "user@example.com", auth.RoleUser)

	for name, tt := range map[string]struct {
		cookie      *http.Cookie
		role        string
		permissions []string
		view        int
		can         string
	}{
		"an administrator":         {admin, auth.RoleAdmin, []string{permExport, permView}, http.StatusNoContent, "yes"},
		"a role with one":          {auditor, "auditor", []string{permView}, http.StatusNoContent, "no"},
		"a role with both":         {analyst, "analyst", []string{permExport, permView}, http.StatusNoContent, "yes"},
		"the user role, with none": {user, auth.RoleUser, []string{}, http.StatusForbidden, "no"},
	} {
		if got := me(t, api, tt.cookie); got.Role != tt.role || !slices.Equal(got.Permissions, tt.permissions) || got.Permissions == nil {
			t.Errorf("%s: role = %q, permissions = %v; want %q and %v", name, got.Role, got.Permissions, tt.role, tt.permissions)
		}
		if w := testutil.Do(t, api, tt.cookie, http.MethodGet, "/view", ""); w.Code != tt.view {
			t.Errorf("%s: GET /view = %d, want %d", name, w.Code, tt.view)
		}
		if got := testutil.Do(t, api, tt.cookie, http.MethodGet, "/can", "").Body.String(); got != tt.can {
			t.Errorf("%s: Can = %s, want %s", name, got, tt.can)
		}
	}

	w := testutil.Do(t, api, user, http.MethodGet, "/view", "")
	if resp := testutil.Decode[testutil.APIError](t, w, http.StatusForbidden); resp.Error.Code != "forbidden" {
		t.Errorf("without the permission: code = %q, want forbidden", resp.Error.Code)
	}
	testutil.Decode[testutil.APIError](t, testutil.Do(t, api, nil, http.MethodGet, "/view", ""), http.StatusUnauthorized)

	// The login answers with the permissions too, so the frontend has them from the first screen.
	body := fmt.Sprintf(`{"email":"analyst@example.com","password":%q}`, testutil.Password)
	logged := testutil.Decode[auth.User](t, testutil.Do(t, api, nil, http.MethodPost, "/auth/login", body), http.StatusOK)
	if !slices.Equal(logged.Permissions, []string{permExport, permView}) {
		t.Errorf("permissions in the login = %v", logged.Permissions)
	}

	// A change to a role applies from the next request of its users, without logging in again.
	if _, err := pool.Exec(t.Context(), "DELETE FROM role_permissions WHERE role = 'auditor'"); err != nil {
		t.Fatal(err)
	}
	if w := testutil.Do(t, api, auditor, http.MethodGet, "/view", ""); w.Code != http.StatusForbidden {
		t.Errorf("after the role lost the permission: GET /view = %d, want 403", w.Code)
	}
}

// A permission stored for a role that the code no longer defines does nothing, and is not shown.
func TestPermissionTheCodeNoLongerDefines(t *testing.T) {
	pool := testutil.NewDB(t)
	api := authAPI(pool)
	testutil.CreateRole(t, pool, "legacy", "invoices.void", permView)
	_, cookie := testutil.Login(t, pool, "old@example.com", "legacy")

	if got := me(t, api, cookie).Permissions; !slices.Equal(got, []string{permView}) {
		t.Errorf("permissions = %v, want only %s", got, permView)
	}
}

// With REQUIRE_ADMIN_TWO_FACTOR, an admin without two-factor authentication has no permission yet,
// and the routes tell the frontend why.
func TestPermissionsOfAnAdminWithoutTwoFactor(t *testing.T) {
	pool := testutil.NewDB(t)
	api := authAPI(pool, func(c *config.Config) { c.RequireAdminTwoFactor = true })
	_, admin := testutil.Login(t, pool, "admin@example.com", auth.RoleAdmin)

	w := testutil.Do(t, api, admin, http.MethodGet, "/view", "")
	if resp := testutil.Decode[testutil.APIError](t, w, http.StatusForbidden); resp.Error.Code != "admin_two_factor_required" {
		t.Errorf("code = %q, want admin_two_factor_required", resp.Error.Code)
	}
	if got := me(t, api, admin); len(got.Permissions) != 0 || got.Role != auth.RoleAdmin {
		t.Errorf("role = %q, permissions = %v; want admin without permissions", got.Role, got.Permissions)
	}
	if got := testutil.Do(t, api, admin, http.MethodGet, "/can", "").Body.String(); got != "no" {
		t.Errorf("Can = %s, want no", got)
	}
}

// A name that is not a permission is a mistake of the code: it stops the startup.
func TestDefinePermissionsRejectsOtherFormats(t *testing.T) {
	authn := auth.NewService(nil, auth.Options{})
	authn.DefinePermissions("orders.refund", "orders.items.edit", "reports_2.view")
	if got := strings.Join(authn.Permissions(), " "); got != "orders.items.edit orders.refund reports_2.view" {
		t.Errorf("permissions = %s", got)
	}
	for _, name := range []string{"orders", "Orders.Refund", "orders refund", "orders.", ".refund", "orders..refund", ""} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("DefinePermissions(%q) should panic", name)
				}
			}()
			authn.DefinePermissions(name)
		}()
	}
}
