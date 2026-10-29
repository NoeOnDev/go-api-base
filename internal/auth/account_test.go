package auth_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/NoeOnDev/go-api-base/internal/auth"
	"github.com/NoeOnDev/go-api-base/internal/testutil"
)

func TestUpdateProfile(t *testing.T) {
	api, pool := newAPI(t)
	_, cookie := testutil.Login(t, pool, "alice@example.com", auth.RoleUser)

	u := decode[auth.User](t, do(t, api, http.MethodPut, "/auth/me", `{"name":"  Alice Smith  "}`, cookie), http.StatusOK)
	if u.Name != "Alice Smith" || u.Email != "alice@example.com" {
		t.Errorf("unexpected user: %+v", u)
	}

	me := decode[auth.User](t, do(t, api, http.MethodGet, "/auth/me", "", cookie), http.StatusOK)
	if me.Name != "Alice Smith" {
		t.Errorf("the change was not saved: %+v", me)
	}

	resp := decode[apiError](t, do(t, api, http.MethodPut, "/auth/me", `{"name":""}`, cookie), http.StatusUnprocessableEntity)
	if resp.Error.Fields["name"] == "" {
		t.Errorf("want an error on name: %+v", resp.Error)
	}

	// The email is not changed through this route: it is an unknown field.
	decode[apiError](t, do(t, api, http.MethodPut, "/auth/me", `{"name":"Alice","email":"other@example.com"}`, cookie), http.StatusBadRequest)

	decode[apiError](t, do(t, api, http.MethodPut, "/auth/me", `{"name":"x"}`, nil), http.StatusUnauthorized)
}

func TestChangePassword(t *testing.T) {
	api, pool := newAPI(t)
	user, current := testutil.Login(t, pool, "alice@example.com", auth.RoleUser)
	other := newSession(t, pool, user.ID) // the same account open on another device

	// Careful: this route has an attempt limit (5 per IP); this test makes 4 requests to it.
	t.Run("without a session", func(t *testing.T) {
		body := `{"current_password":"test-password","password":"a-new-password"}`
		decode[apiError](t, do(t, api, http.MethodPut, "/auth/password", body, nil), http.StatusUnauthorized)
	})

	t.Run("wrong current password", func(t *testing.T) {
		body := `{"current_password":"wrong-password","password":"a-new-password"}`
		resp := decode[apiError](t, do(t, api, http.MethodPut, "/auth/password", body, current), http.StatusUnprocessableEntity)
		if resp.Error.Fields["current_password"] == "" {
			t.Errorf("want an error on current_password: %+v", resp.Error)
		}
	})

	t.Run("short new password", func(t *testing.T) {
		body := `{"current_password":"test-password","password":"short"}`
		resp := decode[apiError](t, do(t, api, http.MethodPut, "/auth/password", body, current), http.StatusUnprocessableEntity)
		if resp.Error.Fields["password"] == "" {
			t.Errorf("want an error on password: %+v", resp.Error)
		}
	})

	body := `{"current_password":"test-password","password":"a-new-password"}`
	if w := do(t, api, http.MethodPut, "/auth/password", body, current); w.Code != http.StatusNoContent {
		t.Fatalf("status = %d; body: %s", w.Code, w.Body)
	}

	// The current session stays active; the one on the other device was closed.
	if w := do(t, api, http.MethodGet, "/auth/me", "", current); w.Code != http.StatusOK {
		t.Errorf("the current session was closed: status = %d", w.Code)
	}
	if w := do(t, api, http.MethodGet, "/auth/me", "", other); w.Code != http.StatusUnauthorized {
		t.Errorf("the other session is still active: status = %d", w.Code)
	}

	// Only the new password works (checked directly, without spending attempts of the limit).
	service := auth.NewService(pool, auth.Options{})
	if _, err := service.Authenticate(t.Context(), "alice@example.com", "a-new-password"); err != nil {
		t.Errorf("login with the new password: %v", err)
	}
	if _, err := service.Authenticate(t.Context(), "alice@example.com", "test-password"); err == nil {
		t.Error("the old password still works")
	}
}

// The same when changing the password from the profile.
func TestChangePasswordIsAtomic(t *testing.T) {
	pool := testutil.NewDB(t)
	api := testutil.NewAPI(pool)
	_, cookie := testutil.Login(t, pool, "alice@example.com", auth.RoleUser)
	testutil.Do(t, api, nil, http.MethodPost, "/auth/login", loginBody) // another session, which would be closed

	exec := func(sql string) error { _, err := pool.Exec(context.Background(), sql); return err }
	failSessionDeletes(t, exec)

	body := fmt.Sprintf(`{"current_password":"test-password","password":%q}`, newPassword)
	testutil.Decode[testutil.APIError](t, testutil.Do(t, api, cookie, http.MethodPut, "/auth/password", body), http.StatusInternalServerError)

	if _, err := auth.NewService(pool, auth.Options{}).Authenticate(t.Context(), "alice@example.com", "test-password"); err != nil {
		t.Errorf("the password should not have changed: %v", err)
	}
}
