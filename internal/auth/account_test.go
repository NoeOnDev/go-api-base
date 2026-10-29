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
	_, cookie := testutil.Login(t, pool, "ana@ejemplo.com", auth.RoleUser)

	u := decode[auth.User](t, do(t, api, http.MethodPut, "/auth/me", `{"name":"  Ana María  "}`, cookie), http.StatusOK)
	if u.Name != "Ana María" || u.Email != "ana@ejemplo.com" {
		t.Errorf("usuario inesperado: %+v", u)
	}

	me := decode[auth.User](t, do(t, api, http.MethodGet, "/auth/me", "", cookie), http.StatusOK)
	if me.Name != "Ana María" {
		t.Errorf("el cambio no se guardó: %+v", me)
	}

	resp := decode[apiError](t, do(t, api, http.MethodPut, "/auth/me", `{"name":""}`, cookie), http.StatusUnprocessableEntity)
	if resp.Error.Fields["name"] == "" {
		t.Errorf("se esperaba error en name: %+v", resp.Error)
	}

	// El email no se cambia por esta ruta: es un campo desconocido.
	decode[apiError](t, do(t, api, http.MethodPut, "/auth/me", `{"name":"Ana","email":"otro@ejemplo.com"}`, cookie), http.StatusBadRequest)

	decode[apiError](t, do(t, api, http.MethodPut, "/auth/me", `{"name":"x"}`, nil), http.StatusUnauthorized)
}

func TestChangePassword(t *testing.T) {
	api, pool := newAPI(t)
	user, current := testutil.Login(t, pool, "ana@ejemplo.com", auth.RoleUser)
	other := newSession(t, pool, user.ID) // la misma cuenta abierta en otro dispositivo

	// Ojo: esta ruta tiene límite de intentos (5 por IP); este test hace 4 peticiones a ella.
	t.Run("sin sesión", func(t *testing.T) {
		body := `{"current_password":"contraseña-de-prueba","password":"una-clave-nueva"}`
		decode[apiError](t, do(t, api, http.MethodPut, "/auth/password", body, nil), http.StatusUnauthorized)
	})

	t.Run("contraseña actual incorrecta", func(t *testing.T) {
		body := `{"current_password":"incorrecta","password":"una-clave-nueva"}`
		resp := decode[apiError](t, do(t, api, http.MethodPut, "/auth/password", body, current), http.StatusUnprocessableEntity)
		if resp.Error.Fields["current_password"] == "" {
			t.Errorf("se esperaba error en current_password: %+v", resp.Error)
		}
	})

	t.Run("contraseña nueva corta", func(t *testing.T) {
		body := `{"current_password":"contraseña-de-prueba","password":"corta"}`
		resp := decode[apiError](t, do(t, api, http.MethodPut, "/auth/password", body, current), http.StatusUnprocessableEntity)
		if resp.Error.Fields["password"] == "" {
			t.Errorf("se esperaba error en password: %+v", resp.Error)
		}
	})

	body := `{"current_password":"contraseña-de-prueba","password":"una-clave-nueva"}`
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
	if _, err := service.Authenticate(t.Context(), "ana@ejemplo.com", "una-clave-nueva"); err != nil {
		t.Errorf("login con la contraseña nueva: %v", err)
	}
	if _, err := service.Authenticate(t.Context(), "ana@ejemplo.com", "contraseña-de-prueba"); err == nil {
		t.Error("la contraseña vieja todavía funciona")
	}
}

// Lo mismo al cambiar la contraseña desde el perfil.
func TestChangePasswordIsAtomic(t *testing.T) {
	pool := testutil.NewDB(t)
	api := testutil.NewAPI(pool)
	_, cookie := testutil.Login(t, pool, "ana@ejemplo.com", auth.RoleUser)
	testutil.Do(t, api, nil, http.MethodPost, "/auth/login", loginBody) // otra sesión, que se cerraría

	exec := func(sql string) error { _, err := pool.Exec(context.Background(), sql); return err }
	failSessionDeletes(t, exec)

	body := fmt.Sprintf(`{"current_password":"contraseña-de-prueba","password":%q}`, newPassword)
	testutil.Decode[testutil.APIError](t, testutil.Do(t, api, cookie, http.MethodPut, "/auth/password", body), http.StatusInternalServerError)

	if _, err := auth.NewService(pool, auth.Options{}).Authenticate(t.Context(), "ana@ejemplo.com", "contraseña-de-prueba"); err != nil {
		t.Errorf("la contraseña no debería haber cambiado: %v", err)
	}
}
