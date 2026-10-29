package categories_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/NoeOnDev/go-api-base/internal/auth"
	"github.com/NoeOnDev/go-api-base/internal/testutil"
)

type category struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Slug string `json:"slug"`
}

type categoryList struct {
	Data []category `json:"data"`
	Meta struct {
		Total int64 `json:"total"`
	} `json:"meta"`
}

func TestCategoriesCRUD(t *testing.T) {
	pool := testutil.NewDB(t)
	api := testutil.NewAPI(pool)
	_, admin := testutil.Login(t, pool, "admin@ejemplo.com", auth.RoleAdmin)

	created := testutil.Decode[category](t,
		testutil.Do(t, api, admin, http.MethodPost, "/categories", `{"name":" Bebidas ","slug":"bebidas"}`),
		http.StatusCreated)
	if created.Name != "Bebidas" || created.Slug != "bebidas" {
		t.Fatalf("categoría inesperada: %+v", created)
	}
	path := fmt.Sprintf("/categories/%d", created.ID)

	// La lectura es pública: no hace falta sesión.
	got := testutil.Decode[category](t, testutil.Do(t, api, nil, http.MethodGet, path, ""), http.StatusOK)
	if got != created {
		t.Errorf("GET = %+v, se esperaba %+v", got, created)
	}

	updated := testutil.Decode[category](t,
		testutil.Do(t, api, admin, http.MethodPut, path, `{"name":"Bebidas frías","slug":"bebidas-frias"}`),
		http.StatusOK)
	if updated.Slug != "bebidas-frias" {
		t.Errorf("categoría editada inesperada: %+v", updated)
	}

	list := testutil.Decode[categoryList](t, testutil.Do(t, api, nil, http.MethodGet, "/categories", ""), http.StatusOK)
	if list.Meta.Total != 1 || len(list.Data) != 1 {
		t.Errorf("listado inesperado: %+v", list)
	}

	if w := testutil.Do(t, api, admin, http.MethodDelete, path, ""); w.Code != http.StatusNoContent {
		t.Fatalf("DELETE status = %d", w.Code)
	}
	testutil.Decode[testutil.APIError](t, testutil.Do(t, api, nil, http.MethodGet, path, ""), http.StatusNotFound)
}

func TestCategoriesWriteRequiresAdmin(t *testing.T) {
	pool := testutil.NewDB(t)
	api := testutil.NewAPI(pool)
	_, user := testutil.Login(t, pool, "user@ejemplo.com", auth.RoleUser)

	body := `{"name":"Bebidas","slug":"bebidas"}`
	testutil.Decode[testutil.APIError](t, testutil.Do(t, api, nil, http.MethodPost, "/categories", body), http.StatusUnauthorized)
	resp := testutil.Decode[testutil.APIError](t, testutil.Do(t, api, user, http.MethodPost, "/categories", body), http.StatusForbidden)
	if resp.Error.Code != "forbidden" {
		t.Errorf("code = %q", resp.Error.Code)
	}
}

func TestCategoriesConflicts(t *testing.T) {
	pool := testutil.NewDB(t)
	api := testutil.NewAPI(pool)
	_, admin := testutil.Login(t, pool, "admin@ejemplo.com", auth.RoleAdmin)

	c := testutil.Decode[category](t,
		testutil.Do(t, api, admin, http.MethodPost, "/categories", `{"name":"Bebidas","slug":"bebidas"}`),
		http.StatusCreated)

	t.Run("slug repetido", func(t *testing.T) {
		resp := testutil.Decode[testutil.APIError](t,
			testutil.Do(t, api, admin, http.MethodPost, "/categories", `{"name":"Otra","slug":"bebidas"}`),
			http.StatusConflict)
		if resp.Error.Code != "slug_taken" || resp.Error.Fields["slug"] == "" {
			t.Errorf("se esperaba el error en slug: %+v", resp.Error)
		}
	})

	t.Run("borrar con productos", func(t *testing.T) {
		product := fmt.Sprintf(`{"category_id":%d,"name":"Café","slug":"cafe","price_cents":1500}`, c.ID)
		testutil.Decode[map[string]any](t, testutil.Do(t, api, admin, http.MethodPost, "/products", product), http.StatusCreated)

		resp := testutil.Decode[testutil.APIError](t,
			testutil.Do(t, api, admin, http.MethodDelete, fmt.Sprintf("/categories/%d", c.ID), ""),
			http.StatusConflict)
		if resp.Error.Code != "category_in_use" {
			t.Errorf("code = %q", resp.Error.Code)
		}
	})
}

func TestCategoriesValidation(t *testing.T) {
	pool := testutil.NewDB(t)
	api := testutil.NewAPI(pool)
	_, admin := testutil.Login(t, pool, "admin@ejemplo.com", auth.RoleAdmin)

	resp := testutil.Decode[testutil.APIError](t,
		testutil.Do(t, api, admin, http.MethodPost, "/categories", `{"name":"","slug":"Con Espacios"}`),
		http.StatusUnprocessableEntity)
	if resp.Error.Fields["name"] == "" || resp.Error.Fields["slug"] == "" {
		t.Errorf("se esperaban errores en name y slug: %+v", resp.Error)
	}
}
