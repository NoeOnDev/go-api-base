package auth_test

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/NoeOnDev/go-api-base/internal/auth"
	"github.com/NoeOnDev/go-api-base/internal/testutil"
)

type roleList struct {
	Data []auth.Role `json:"data"`
}

func roleDefinition(key, name, password string, permissions ...string) string {
	return fmt.Sprintf(`{"key":%q,"name":%q,"password":%q,"permissions":["%s"]}`, key, name, password, strings.Join(permissions, `","`))
}

// setRole gives the user a role, as PUT /admin/users/{id}/role does, without going through the user
// management (which a project can remove).
func setRole(t *testing.T, pool *pgxpool.Pool, userID int64, role string) {
	t.Helper()
	if _, err := pool.Exec(t.Context(), "UPDATE users SET role = $2 WHERE id = $1", userID, role); err != nil {
		t.Fatalf("give the role %s: %v", role, err)
	}
}

func TestRoles(t *testing.T) {
	pool := testutil.NewDB(t)
	api := authAPI(pool)
	_, admin := testutil.Login(t, pool, "admin@example.com", auth.RoleAdmin)
	alice, aliceCookie := testutil.Login(t, pool, "alice@example.com", auth.RoleUser)

	// The permissions a role can have are the ones the code defines.
	perms := testutil.Decode[struct {
		Data []string `json:"data"`
	}](t, testutil.Do(t, api, admin, http.MethodGet, "/admin/permissions", ""), http.StatusOK)
	if !slices.Equal(perms.Data, []string{permExport, permView}) {
		t.Errorf("permissions = %v", perms.Data)
	}

	// The two roles of the template: admin has every permission, user none.
	roles := testutil.Decode[roleList](t, testutil.Do(t, api, admin, http.MethodGet, "/admin/roles", ""), http.StatusOK).Data
	if len(roles) != 2 || roles[0].Key != auth.RoleAdmin || roles[1].Key != auth.RoleUser || !roles[0].BuiltIn || !roles[1].BuiltIn ||
		!slices.Equal(roles[0].Permissions, []string{permExport, permView}) || len(roles[1].Permissions) != 0 || roles[0].Users != 1 || roles[1].Users != 1 {
		t.Errorf("roles = %+v", roles)
	}

	// Creating one. A repeated permission counts once.
	w := testutil.Do(t, api, admin, http.MethodPost, "/admin/roles", roleDefinition("auditor", "Auditor", "", permView, permView))
	role := testutil.Decode[auth.Role](t, w, http.StatusCreated)
	if role.Key != "auditor" || role.Name != "Auditor" || role.BuiltIn || !slices.Equal(role.Permissions, []string{permView}) || role.Users != 0 {
		t.Errorf("role = %+v", role)
	}
	if w.Header().Get("Location") != "/admin/roles/auditor" {
		t.Errorf("Location = %q", w.Header().Get("Location"))
	}

	for name, tt := range map[string]struct{ body, field string }{
		"a key with other characters": {roleDefinition("Sales Team", "Sales", "", permView), "key"},
		"a key that is too short":     {roleDefinition("a", "A", "", permView), "key"},
		"without a name":              {roleDefinition("sales", " ", "", permView), "name"},
		"a permission nobody defined": {roleDefinition("sales", "Sales", "", "invoices.void"), "permissions"},
	} {
		w := testutil.Do(t, api, admin, http.MethodPost, "/admin/roles", tt.body)
		if resp := testutil.Decode[testutil.APIError](t, w, http.StatusUnprocessableEntity); resp.Error.Fields[tt.field] == "" {
			t.Errorf("%s: want an error on %s: %+v", name, tt.field, resp.Error)
		}
	}
	for _, key := range []string{"auditor", auth.RoleAdmin} {
		w := testutil.Do(t, api, admin, http.MethodPost, "/admin/roles", roleDefinition(key, "Again", "", permView))
		if resp := testutil.Decode[testutil.APIError](t, w, http.StatusConflict); resp.Error.Code != "role_taken" {
			t.Errorf("a second %s: code = %q, want role_taken", key, resp.Error.Code)
		}
	}
	// A role without permissions is fine: it only names a group of users.
	testutil.Decode[auth.Role](t, testutil.Do(t, api, admin, http.MethodPost, "/admin/roles", `{"key":"guest","name":"Guest"}`), http.StatusCreated)

	// Giving it to a user: from the next request they can do what the role says.
	if w := testutil.Do(t, api, aliceCookie, http.MethodGet, "/view", ""); w.Code != http.StatusForbidden {
		t.Fatalf("before the role: GET /view = %d", w.Code)
	}
	setRole(t, pool, alice.ID, "auditor")
	if w := testutil.Do(t, api, aliceCookie, http.MethodGet, "/view", ""); w.Code != http.StatusNoContent {
		t.Errorf("with the role: GET /view = %d, want 204", w.Code)
	}

	// Changing its permissions asks for the administrator's password, and applies at once.
	change := roleDefinition("", "Internal auditor", testutil.Password, permView, permExport)
	for name, body := range map[string]string{
		"without the password": roleDefinition("", "Internal auditor", "", permExport),
		"with a wrong one":     roleDefinition("", "Internal auditor", "not-the-password", permExport),
	} {
		w := testutil.Do(t, api, admin, http.MethodPut, "/admin/roles/auditor", body)
		if resp := testutil.Decode[testutil.APIError](t, w, http.StatusUnprocessableEntity); resp.Error.Fields["password"] == "" {
			t.Errorf("%s: want an error on password: %+v", name, resp.Error)
		}
	}
	role = testutil.Decode[auth.Role](t, testutil.Do(t, api, admin, http.MethodPut, "/admin/roles/auditor", change), http.StatusOK)
	if role.Name != "Internal auditor" || !slices.Equal(role.Permissions, []string{permExport, permView}) || role.Users != 1 {
		t.Errorf("role after the change = %+v", role)
	}
	if got := testutil.Do(t, api, aliceCookie, http.MethodGet, "/can", "").Body.String(); got != "yes" {
		t.Errorf("after the change, Can = %s, want yes", got)
	}
	testutil.Decode[testutil.APIError](t, testutil.Do(t, api, admin, http.MethodPut, "/admin/roles/nobody", change), http.StatusNotFound)

	// The roles of the template are not changed nor deleted.
	for _, key := range []string{auth.RoleAdmin, auth.RoleUser} {
		for _, method := range []string{http.MethodPut, http.MethodDelete} {
			w := testutil.Do(t, api, admin, method, "/admin/roles/"+key, change)
			if resp := testutil.Decode[testutil.APIError](t, w, http.StatusConflict); resp.Error.Code != "built_in_role" {
				t.Errorf("%s %s: code = %q, want built_in_role", method, key, resp.Error.Code)
			}
		}
	}

	// A role that users have is not deleted: they get another one first.
	w = testutil.Do(t, api, admin, http.MethodDelete, "/admin/roles/auditor", "")
	if resp := testutil.Decode[testutil.APIError](t, w, http.StatusConflict); resp.Error.Code != "role_in_use" {
		t.Errorf("code = %q, want role_in_use", resp.Error.Code)
	}
	setRole(t, pool, alice.ID, auth.RoleUser)
	if w := testutil.Do(t, api, admin, http.MethodDelete, "/admin/roles/auditor", ""); w.Code != http.StatusNoContent {
		t.Fatalf("delete: status = %d; body: %s", w.Code, w.Body)
	}
	testutil.Decode[testutil.APIError](t, testutil.Do(t, api, admin, http.MethodDelete, "/admin/roles/auditor", ""), http.StatusNotFound)
	// A role that no longer exists cannot be given: the database refuses it.
	if _, err := pool.Exec(t.Context(), "UPDATE users SET role = 'auditor' WHERE id = $1", alice.ID); err == nil {
		t.Error("a user got a role that does not exist")
	}

	// Only the administrators manage the roles, whatever permissions another role has.
	testutil.CreateRole(t, pool, "powerful", permView, permExport)
	_, powerful := testutil.Login(t, pool, "powerful@example.com", "powerful")
	for _, path := range []string{"/admin/roles", "/admin/permissions"} {
		testutil.Decode[testutil.APIError](t, testutil.Do(t, api, powerful, http.MethodGet, path, ""), http.StatusForbidden)
		testutil.Decode[testutil.APIError](t, testutil.Do(t, api, nil, http.MethodGet, path, ""), http.StatusUnauthorized)
	}
	testutil.Decode[testutil.APIError](t, testutil.Do(t, api, powerful, http.MethodPost, "/admin/roles", roleDefinition("mine", "Mine", "", permView)), http.StatusForbidden)
}
