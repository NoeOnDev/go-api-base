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
		t.Errorf("usuario inesperado: %+v", u)
	}

	me := decode[auth.User](t, do(t, api, http.MethodGet, "/auth/me", "", cookie), http.StatusOK)
	if me.Name != "Alice Smith" {
		t.Errorf("el cambio no se guardó: %+v", me)
	}

	resp := decode[apiError](t, do(t, api, http.MethodPut, "/auth/me", `{"name":""}`, cookie), http.StatusUnprocessableEntity)
	if resp.Error.Fields["name"] == "" {
		t.Errorf("se esperaba error en name: %+v", resp.Error)
	}

	// El email no se cambia por esta ruta: es un campo desconocido.
	decode[apiError](t, do(t, api, http.MethodPut, "/auth/me", `{"name":"Alice","email":"other@example.com"}`, cookie), http.StatusBadRequest)

	decode[apiError](t, do(t, api, http.MethodPut, "/auth/me", `{"name":"x"}`, nil), http.StatusUnauthorized)
}

func TestChangePassword(t *testing.T) {
	api, pool := newAPI(t)
	user, current := testutil.Login(t, pool, "alice@example.com", auth.RoleUser)
	other := newSession(t, pool, user.ID) // la misma cuenta abierta en otro dispositivo

	// Ojo: esta ruta tiene límite de intentos (5 por IP); este test hace 4 peticiones a ella.
	t.Run("sin sesión", func(t *testing.T) {
		body := `{"current_password":"test-password","password":"a-new-password"}`
		decode[apiError](t, do(t, api, http.MethodPut, "/auth/password", body, nil), http.StatusUnauthorized)
	})

	t.Run("contraseña actual incorrecta", func(t *testing.T) {
		body := `{"current_password":"wrong-password","password":"a-new-password"}`
		resp := decode[apiError](t, do(t, api, http.MethodPut, "/auth/password", body, current), http.StatusUnprocessableEntity)
		if resp.Error.Fields["current_password"] == "" {
			t.Errorf("se esperaba error en current_password: %+v", resp.Error)
		}
	})

	t.Run("contraseña nueva corta", func(t *testing.T) {
		body := `{"current_password":"test-password","password":"short"}`
		resp := decode[apiError](t, do(t, api, http.MethodPut, "/auth/password", body, current), http.StatusUnprocessableEntity)
		if resp.Error.Fields["password"] == "" {
			t.Errorf("se esperaba error en password: %+v", resp.Error)
		}
	})

	body := `{"current_password":"test-password","password":"a-new-password"}`
	if w := do(t, api, http.MethodPut, "/auth/password", body, current); w.Code != http.StatusNoContent {
		t.Fatalf("status = %d; cuerpo: %s", w.Code, w.Body)
	}

	// La sesión actual sigue activa; la del otro dispositivo se cerró.
	if w := do(t, api, http.MethodGet, "/auth/me", "", current); w.Code != http.StatusOK {
		t.Errorf("la sesión actual se cerró: status = %d", w.Code)
	}
	if w := do(t, api, http.MethodGet, "/auth/me", "", other); w.Code != http.StatusUnauthorized {
		t.Errorf("la otra sesión sigue activa: status = %d", w.Code)
	}

	// Solo funciona la contraseña nueva (se comprueba directo, sin gastar intentos del límite).
	service := auth.NewService(pool, auth.Options{})
	if _, err := service.Authenticate(t.Context(), "alice@example.com", "a-new-password"); err != nil {
		t.Errorf("login con la contraseña nueva: %v", err)
	}
	if _, err := service.Authenticate(t.Context(), "alice@example.com", "test-password"); err == nil {
		t.Error("la contraseña vieja todavía funciona")
	}
}

// Lo mismo al cambiar la contraseña desde el perfil.
func TestChangePasswordIsAtomic(t *testing.T) {
	pool := testutil.NewDB(t)
	api := testutil.NewAPI(pool)
	_, cookie := testutil.Login(t, pool, "alice@example.com", auth.RoleUser)
	testutil.Do(t, api, nil, http.MethodPost, "/auth/login", loginBody) // otra sesión, que se cerraría

	exec := func(sql string) error { _, err := pool.Exec(context.Background(), sql); return err }
	failSessionDeletes(t, exec)

	body := fmt.Sprintf(`{"current_password":"test-password","password":%q}`, newPassword)
	testutil.Decode[testutil.APIError](t, testutil.Do(t, api, cookie, http.MethodPut, "/auth/password", body), http.StatusInternalServerError)

	if _, err := auth.NewService(pool, auth.Options{}).Authenticate(t.Context(), "alice@example.com", "test-password"); err != nil {
		t.Errorf("la contraseña no debería haber cambiado: %v", err)
	}
}
