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
	_, admin := testutil.Login(t, pool, "admin@example.com", auth.RoleAdmin)

	created := testutil.Decode[category](t,
		testutil.Do(t, api, admin, http.MethodPost, "/categories", `{"name":" Drinks ","slug":"drinks"}`),
		http.StatusCreated)
	if created.Name != "Drinks" || created.Slug != "drinks" {
		t.Fatalf("unexpected category: %+v", created)
	}
	path := fmt.Sprintf("/categories/%d", created.ID)

	// Reads are public: no session is needed.
	got := testutil.Decode[category](t, testutil.Do(t, api, nil, http.MethodGet, path, ""), http.StatusOK)
	if got != created {
		t.Errorf("GET = %+v, want %+v", got, created)
	}

	updated := testutil.Decode[category](t,
		testutil.Do(t, api, admin, http.MethodPut, path, `{"name":"Cold drinks","slug":"cold-drinks"}`),
		http.StatusOK)
	if updated.Slug != "cold-drinks" {
		t.Errorf("unexpected updated category: %+v", updated)
	}

	list := testutil.Decode[categoryList](t, testutil.Do(t, api, nil, http.MethodGet, "/categories", ""), http.StatusOK)
	if list.Meta.Total != 1 || len(list.Data) != 1 {
		t.Errorf("unexpected list: %+v", list)
	}

	if w := testutil.Do(t, api, admin, http.MethodDelete, path, ""); w.Code != http.StatusNoContent {
		t.Fatalf("DELETE status = %d", w.Code)
	}
	testutil.Decode[testutil.APIError](t, testutil.Do(t, api, nil, http.MethodGet, path, ""), http.StatusNotFound)
}

func TestCategoriesWriteRequiresAdmin(t *testing.T) {
	pool := testutil.NewDB(t)
	api := testutil.NewAPI(pool)
	_, user := testutil.Login(t, pool, "user@example.com", auth.RoleUser)

	body := `{"name":"Drinks","slug":"drinks"}`
	testutil.Decode[testutil.APIError](t, testutil.Do(t, api, nil, http.MethodPost, "/categories", body), http.StatusUnauthorized)
	resp := testutil.Decode[testutil.APIError](t, testutil.Do(t, api, user, http.MethodPost, "/categories", body), http.StatusForbidden)
	if resp.Error.Code != "forbidden" {
		t.Errorf("code = %q", resp.Error.Code)
	}
}

func TestCategoriesConflicts(t *testing.T) {
	pool := testutil.NewDB(t)
	api := testutil.NewAPI(pool)
	_, admin := testutil.Login(t, pool, "admin@example.com", auth.RoleAdmin)

	c := testutil.Decode[category](t,
		testutil.Do(t, api, admin, http.MethodPost, "/categories", `{"name":"Drinks","slug":"drinks"}`),
		http.StatusCreated)

	t.Run("repeated slug", func(t *testing.T) {
		resp := testutil.Decode[testutil.APIError](t,
			testutil.Do(t, api, admin, http.MethodPost, "/categories", `{"name":"Other","slug":"drinks"}`),
			http.StatusConflict)
		if resp.Error.Code != "slug_taken" || resp.Error.Fields["slug"] == "" {
			t.Errorf("want the error in slug: %+v", resp.Error)
		}
	})

	t.Run("delete with products", func(t *testing.T) {
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
	_, admin := testutil.Login(t, pool, "admin@example.com", auth.RoleAdmin)

	resp := testutil.Decode[testutil.APIError](t,
		testutil.Do(t, api, admin, http.MethodPost, "/categories", `{"name":"","slug":"With Spaces"}`),
		http.StatusUnprocessableEntity)
	if resp.Error.Fields["name"] == "" || resp.Error.Fields["slug"] == "" {
		t.Errorf("want errors in name and slug: %+v", resp.Error)
	}
}
