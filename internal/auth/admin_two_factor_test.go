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
	_, admin := testutil.Login(t, pool, "admin@example.com", auth.RoleAdmin)

	// An inactive product, created through an API without the option: only admins see it.
	plain := testutil.NewAPI(pool)
	category := testutil.Decode[struct{ ID int64 }](t,
		testutil.Do(t, plain, admin, http.MethodPost, "/categories", `{"name":"Drinks","slug":"drinks"}`), http.StatusCreated)
	body := fmt.Sprintf(`{"category_id":%d,"name":"Old tea","slug":"old-tea","price_cents":100,"stock":0,"active":false}`, category.ID)
	product := testutil.Decode[struct{ ID int64 }](t,
		testutil.Do(t, plain, admin, http.MethodPost, "/products", body), http.StatusCreated)
	productPath := fmt.Sprintf("/products/%d", product.ID)

	w := testutil.Do(t, api, admin, http.MethodPost, "/categories", `{"name":"Food","slug":"food"}`)
	if resp := testutil.Decode[testutil.APIError](t, w, http.StatusForbidden); resp.Error.Code != "admin_two_factor_required" {
		t.Errorf("code = %q, want admin_two_factor_required", resp.Error.Code)
	}
	if w := testutil.Do(t, api, admin, http.MethodGet, productPath, ""); w.Code != http.StatusNotFound {
		t.Errorf("inactive product without two-factor authentication: status = %d, want 404", w.Code)
	}

	// The account routes only ask for a session: the admin can turn it on.
	if me := testutil.Decode[auth.User](t, testutil.Do(t, api, admin, http.MethodGet, "/auth/me", ""), http.StatusOK); me.Role != auth.RoleAdmin {
		t.Errorf("role = %q, want admin", me.Role)
	}
	testutil.EnableTwoFactor(t, api, admin, testutil.Password)

	testutil.Decode[struct{ ID int64 }](t,
		testutil.Do(t, api, admin, http.MethodPost, "/categories", `{"name":"Food","slug":"food"}`), http.StatusCreated)
	if w := testutil.Do(t, api, admin, http.MethodGet, productPath, ""); w.Code != http.StatusOK {
		t.Errorf("inactive product with two-factor authentication: status = %d, want 200", w.Code)
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
