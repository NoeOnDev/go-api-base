package auth_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/NoeOnDev/go-api-base/internal/auth"
	"github.com/NoeOnDev/go-api-base/internal/mail"
	"github.com/NoeOnDev/go-api-base/internal/testutil"
)

type adminUserList struct {
	Data []auth.AdminUser `json:"data"`
	Meta struct {
		Total int64 `json:"total"`
	} `json:"meta"`
}

func passwordBody(password string) string { return fmt.Sprintf(`{"password":%q}`, password) }

func roleBody(role, password string) string {
	return fmt.Sprintf(`{"role":%q,"password":%q}`, role, password)
}

func userPath(id int64, action string) string { return fmt.Sprintf("/admin/users/%d%s", id, action) }

func TestAdminListUsers(t *testing.T) {
	pool := testutil.NewDB(t)
	api := testutil.NewAPI(pool)
	_, admin := testutil.Login(t, pool, "admin@example.com", auth.RoleAdmin)
	alice, _ := testutil.Login(t, pool, "alice@example.com", auth.RoleUser)
	testutil.Login(t, pool, "bob@example.com", auth.RoleUser)

	list := func(query string) adminUserList {
		t.Helper()
		return testutil.Decode[adminUserList](t, testutil.Do(t, api, admin, http.MethodGet, "/admin/users"+query, ""), http.StatusOK)
	}

	if all := list(""); all.Meta.Total != 3 || len(all.Data) != 3 || all.Data[0].Email != "bob@example.com" {
		t.Errorf("all users, newest first: %+v", all)
	}
	if found := list("?q=ALICE"); found.Meta.Total != 1 || found.Data[0].ID != alice.ID || found.Data[0].Status != auth.StatusActive {
		t.Errorf("search by email: %+v", found)
	}
	if found := list("?q=%25"); found.Meta.Total != 0 {
		t.Errorf("a %% is searched as text, not as a wildcard: %+v", found)
	}
	if admins := list("?role=admin"); admins.Meta.Total != 1 || admins.Data[0].Email != "admin@example.com" {
		t.Errorf("filter by role: %+v", admins)
	}

	testutil.Decode[auth.AdminUser](t, testutil.Do(t, api, admin, http.MethodPost, userPath(alice.ID, "/suspend"), ""), http.StatusOK)
	if suspended := list("?status=suspended"); suspended.Meta.Total != 1 || suspended.Data[0].ID != alice.ID {
		t.Errorf("filter by status: %+v", suspended)
	}

	for _, query := range []string{"?role=owner", "?status=gone"} {
		w := testutil.Do(t, api, admin, http.MethodGet, "/admin/users"+query, "")
		testutil.Decode[testutil.APIError](t, w, http.StatusUnprocessableEntity)
	}

	got := testutil.Decode[auth.AdminUser](t, testutil.Do(t, api, admin, http.MethodGet, userPath(alice.ID, ""), ""), http.StatusOK)
	if got.Email != "alice@example.com" || got.Status != auth.StatusSuspended || got.SuspendedAt == nil {
		t.Errorf("detail: %+v", got)
	}
	w := testutil.Do(t, api, admin, http.MethodGet, userPath(999999, ""), "")
	if resp := testutil.Decode[testutil.APIError](t, w, http.StatusNotFound); resp.Error.Code != "user_not_found" {
		t.Errorf("code = %q, want user_not_found", resp.Error.Code)
	}

	// Only for admins.
	testutil.Decode[testutil.APIError](t, testutil.Do(t, api, nil, http.MethodGet, "/admin/users", ""), http.StatusUnauthorized)
	_, carol := testutil.Login(t, pool, "carol@example.com", auth.RoleUser)
	testutil.Decode[testutil.APIError](t, testutil.Do(t, api, carol, http.MethodGet, "/admin/users", ""), http.StatusForbidden)
}

func TestAdminChangeRole(t *testing.T) {
	pool := testutil.NewDB(t)
	api := testutil.NewAPI(pool)
	admin, adminCookie := testutil.Login(t, pool, "admin@example.com", auth.RoleAdmin)
	alice, aliceCookie := testutil.Login(t, pool, "alice@example.com", auth.RoleUser)

	// It asks for the admin's password and a valid role.
	for name, tt := range map[string]struct{ body, field string }{
		"wrong password": {roleBody(auth.RoleAdmin, "wrong-password"), "password"},
		"unknown role":   {roleBody("owner", testutil.Password), "role"},
	} {
		w := testutil.Do(t, api, adminCookie, http.MethodPut, userPath(alice.ID, "/role"), tt.body)
		if resp := testutil.Decode[testutil.APIError](t, w, http.StatusUnprocessableEntity); resp.Error.Fields[tt.field] == "" {
			t.Errorf("%s: want an error on %s: %+v", name, tt.field, resp.Error)
		}
	}

	w := testutil.Do(t, api, adminCookie, http.MethodPut, userPath(alice.ID, "/role"), roleBody(auth.RoleAdmin, testutil.Password))
	if got := testutil.Decode[auth.AdminUser](t, w, http.StatusOK); got.Role != auth.RoleAdmin {
		t.Errorf("role = %q, want admin", got.Role)
	}
	// The change applies at once: Alice's session reaches the admin routes.
	testutil.Decode[adminUserList](t, testutil.Do(t, api, aliceCookie, http.MethodGet, "/admin/users", ""), http.StatusOK)

	// Not on their own account, and 404 if the user does not exist.
	w = testutil.Do(t, api, adminCookie, http.MethodPut, userPath(admin.ID, "/role"), roleBody(auth.RoleUser, testutil.Password))
	if resp := testutil.Decode[testutil.APIError](t, w, http.StatusConflict); resp.Error.Code != "own_account" {
		t.Errorf("code = %q, want own_account", resp.Error.Code)
	}
	w = testutil.Do(t, api, adminCookie, http.MethodPut, userPath(999999, "/role"), roleBody(auth.RoleUser, testutil.Password))
	testutil.Decode[testutil.APIError](t, w, http.StatusNotFound)
}

func TestAdminSuspendUser(t *testing.T) {
	pool := testutil.NewDB(t)
	mailer := &mail.Recorder{}
	api := testutil.NewAPI(pool, testutil.WithMailer(mailer))
	admin, adminCookie := testutil.Login(t, pool, "admin@example.com", auth.RoleAdmin)
	alice, aliceCookie := testutil.Login(t, pool, "alice@example.com", auth.RoleUser)

	w := testutil.Do(t, api, adminCookie, http.MethodPost, userPath(alice.ID, "/suspend"), "")
	if got := testutil.Decode[auth.AdminUser](t, w, http.StatusOK); got.Status != auth.StatusSuspended || got.SuspendedAt == nil {
		t.Errorf("suspended user: %+v", got)
	}

	// Her session stopped working, and she cannot log in again.
	testutil.Decode[testutil.APIError](t, testutil.Do(t, api, aliceCookie, http.MethodGet, "/auth/me", ""), http.StatusUnauthorized)
	w = testutil.Do(t, api, nil, http.MethodPost, "/auth/login", loginBodyFor("alice@example.com", testutil.Password))
	if resp := testutil.Decode[testutil.APIError](t, w, http.StatusForbidden); resp.Error.Code != "account_suspended" {
		t.Errorf("login: code = %q, want account_suspended", resp.Error.Code)
	}
	// With the wrong password, the usual answer: it does not reveal that the account is suspended.
	w = testutil.Do(t, api, nil, http.MethodPost, "/auth/login", loginBodyFor("alice@example.com", "wrong-password"))
	testutil.Decode[testutil.APIError](t, w, http.StatusUnauthorized)

	// "Forgot my password" answers as always, but sends nothing.
	w = testutil.Do(t, api, nil, http.MethodPost, "/auth/password/forgot", `{"email":"alice@example.com"}`)
	if w.Code != http.StatusAccepted {
		t.Errorf("forgot: status = %d", w.Code)
	}
	if _, ok := mailer.Last("alice@example.com"); ok {
		t.Error("a suspended account should not get a reset link")
	}

	w = testutil.Do(t, api, adminCookie, http.MethodPost, userPath(alice.ID, "/unsuspend"), "")
	if got := testutil.Decode[auth.AdminUser](t, w, http.StatusOK); got.Status != auth.StatusActive || got.SuspendedAt != nil {
		t.Errorf("unsuspended user: %+v", got)
	}
	w = testutil.Do(t, api, nil, http.MethodPost, "/auth/login", loginBodyFor("alice@example.com", testutil.Password))
	testutil.Decode[auth.User](t, w, http.StatusOK)

	// Not their own account, and 404 if the user does not exist.
	w = testutil.Do(t, api, adminCookie, http.MethodPost, userPath(admin.ID, "/suspend"), "")
	if resp := testutil.Decode[testutil.APIError](t, w, http.StatusConflict); resp.Error.Code != "own_account" {
		t.Errorf("code = %q, want own_account", resp.Error.Code)
	}
	testutil.Decode[testutil.APIError](t, testutil.Do(t, api, adminCookie, http.MethodPost, userPath(999999, "/suspend"), ""), http.StatusNotFound)
	testutil.Decode[testutil.APIError](t, testutil.Do(t, api, adminCookie, http.MethodPost, userPath(999999, "/unsuspend"), ""), http.StatusNotFound)
}

// A suspended admin does not count: the only active one cannot delete their account.
func TestSuspendedAdminIsNotCountedAsAdmin(t *testing.T) {
	pool := testutil.NewDB(t)
	api := testutil.NewAPI(pool)
	_, first := testutil.Login(t, pool, "first@example.com", auth.RoleAdmin)
	second, _ := testutil.Login(t, pool, "second@example.com", auth.RoleAdmin)

	testutil.Decode[auth.AdminUser](t, testutil.Do(t, api, first, http.MethodPost, userPath(second.ID, "/suspend"), ""), http.StatusOK)

	w := testutil.Do(t, api, first, http.MethodDelete, "/auth/me", passwordBody(testutil.Password))
	if resp := testutil.Decode[testutil.APIError](t, w, http.StatusConflict); resp.Error.Code != "last_admin" {
		t.Errorf("code = %q, want last_admin", resp.Error.Code)
	}
}

func TestAdminDeleteUser(t *testing.T) {
	pool := testutil.NewDB(t)
	api := testutil.NewAPI(pool)
	admin, adminCookie := testutil.Login(t, pool, "admin@example.com", auth.RoleAdmin)
	alice, aliceCookie := testutil.Login(t, pool, "alice@example.com", auth.RoleUser)

	w := testutil.Do(t, api, adminCookie, http.MethodDelete, userPath(alice.ID, ""), passwordBody("wrong-password"))
	if resp := testutil.Decode[testutil.APIError](t, w, http.StatusUnprocessableEntity); resp.Error.Fields["password"] == "" {
		t.Errorf("want an error on password: %+v", resp.Error)
	}

	if w := testutil.Do(t, api, adminCookie, http.MethodDelete, userPath(alice.ID, ""), passwordBody(testutil.Password)); w.Code != http.StatusNoContent {
		t.Fatalf("delete: status = %d; body: %s", w.Code, w.Body)
	}
	testutil.Decode[testutil.APIError](t, testutil.Do(t, api, adminCookie, http.MethodGet, userPath(alice.ID, ""), ""), http.StatusNotFound)
	testutil.Decode[testutil.APIError](t, testutil.Do(t, api, aliceCookie, http.MethodGet, "/auth/me", ""), http.StatusUnauthorized)

	w = testutil.Do(t, api, adminCookie, http.MethodDelete, userPath(admin.ID, ""), passwordBody(testutil.Password))
	if resp := testutil.Decode[testutil.APIError](t, w, http.StatusConflict); resp.Error.Code != "own_account" {
		t.Errorf("code = %q, want own_account", resp.Error.Code)
	}
	w = testutil.Do(t, api, adminCookie, http.MethodDelete, userPath(999999, ""), passwordBody(testutil.Password))
	testutil.Decode[testutil.APIError](t, w, http.StatusNotFound)
}
