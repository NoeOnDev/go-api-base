package products_test

import (
	"fmt"
	"net/http"
	"net/url"
	"testing"

	"github.com/NoeOnDev/go-api-base/internal/auth"
	"github.com/NoeOnDev/go-api-base/internal/testutil"
)

type product struct {
	ID       int64 `json:"id"`
	Category struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	} `json:"category"`
	Name       string `json:"name"`
	Slug       string `json:"slug"`
	PriceCents int64  `json:"price_cents"`
	Stock      int32  `json:"stock"`
	Active     bool   `json:"active"`
}

type productList struct {
	Data []product `json:"data"`
	Meta struct {
		Total int64 `json:"total"`
	} `json:"meta"`
}

// catalog prepares the database with an admin, a regular user and two categories.
type catalog struct {
	t      *testing.T
	api    http.Handler
	admin  *http.Cookie
	user   *http.Cookie
	drinks int64
	food   int64
}

func newCatalog(t *testing.T) *catalog {
	t.Helper()
	pool := testutil.NewDB(t)
	c := &catalog{t: t, api: testutil.NewAPI(pool)}
	_, c.admin = testutil.Login(t, pool, "admin@example.com", auth.RoleAdmin)
	_, c.user = testutil.Login(t, pool, "user@example.com", auth.RoleUser)
	c.drinks = c.createCategory("Drinks", "drinks")
	c.food = c.createCategory("Food", "food")
	return c
}

func (c *catalog) createCategory(name, slug string) int64 {
	c.t.Helper()
	body := fmt.Sprintf(`{"name":%q,"slug":%q}`, name, slug)
	resp := testutil.Decode[struct{ ID int64 }](c.t, testutil.Do(c.t, c.api, c.admin, http.MethodPost, "/categories", body), http.StatusCreated)
	return resp.ID
}

func (c *catalog) createProduct(categoryID int64, name, slug string, active bool) product {
	c.t.Helper()
	body := fmt.Sprintf(`{"category_id":%d,"name":%q,"slug":%q,"price_cents":1250,"stock":10,"active":%t}`,
		categoryID, name, slug, active)
	return testutil.Decode[product](c.t, testutil.Do(c.t, c.api, c.admin, http.MethodPost, "/products", body), http.StatusCreated)
}

// list requests the list with the given session (nil = anonymous visitor).
func (c *catalog) list(cookie *http.Cookie, query string) productList {
	c.t.Helper()
	return testutil.Decode[productList](c.t, testutil.Do(c.t, c.api, cookie, http.MethodGet, "/products"+query, ""), http.StatusOK)
}

func TestProductsCRUD(t *testing.T) {
	c := newCatalog(t)

	// active is not sent: it defaults to true. The response includes the category (JOIN).
	body := fmt.Sprintf(`{"category_id":%d,"name":"Café au lait","slug":"cafe-au-lait","price_cents":1250,"stock":5}`, c.drinks)
	p := testutil.Decode[product](t, testutil.Do(t, c.api, c.admin, http.MethodPost, "/products", body), http.StatusCreated)
	if p.Category.ID != c.drinks || p.Category.Name != "Drinks" || !p.Active || p.PriceCents != 1250 {
		t.Fatalf("unexpected product: %+v", p)
	}
	path := fmt.Sprintf("/products/%d", p.ID)

	// Public reads.
	got := testutil.Decode[product](t, testutil.Do(t, c.api, nil, http.MethodGet, path, ""), http.StatusOK)
	if got != p {
		t.Errorf("GET = %+v, want %+v", got, p)
	}

	// Update: it changes category and is deactivated.
	body = fmt.Sprintf(`{"category_id":%d,"name":"Espresso beans","slug":"espresso-beans","price_cents":1800,"stock":0,"active":false}`, c.food)
	updated := testutil.Decode[product](t, testutil.Do(t, c.api, c.admin, http.MethodPut, path, body), http.StatusOK)
	if updated.Category.Name != "Food" || updated.Active || updated.Slug != "espresso-beans" {
		t.Errorf("unexpected updated product: %+v", updated)
	}

	if w := testutil.Do(t, c.api, c.admin, http.MethodDelete, path, ""); w.Code != http.StatusNoContent {
		t.Fatalf("DELETE status = %d", w.Code)
	}
	testutil.Decode[testutil.APIError](t, testutil.Do(t, c.api, nil, http.MethodGet, path, ""), http.StatusNotFound)
}

// The filters are tested as admin, who also sees the inactive products.
func TestProductsFilters(t *testing.T) {
	c := newCatalog(t)
	c.createProduct(c.drinks, "Café au lait", "cafe-au-lait", true)
	c.createProduct(c.drinks, "Green tea", "green-tea", false)
	c.createProduct(c.food, "CAFÉ cake", "cafe-cake", true)
	c.createProduct(c.food, "Discount 50%", "discount-50", true)

	tests := []struct {
		name  string
		query string
		want  int64
	}{
		{"no filters", "", 4},
		{"by category", fmt.Sprintf("?category_id=%d", c.drinks), 2},
		{"case-insensitive search", "?q=" + url.QueryEscape("café"), 2},
		{"only active", "?active=true", 3},
		{"combined filters", fmt.Sprintf("?category_id=%d&active=true", c.drinks), 1},
		{"% is searched as text", "?q=" + url.QueryEscape("50%"), 1},
		{"% is not a wildcard", "?q=" + url.QueryEscape("%"), 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			list := c.list(c.admin, tt.query)
			if list.Meta.Total != tt.want || int64(len(list.Data)) != tt.want {
				t.Errorf("total = %d, items = %d, want %d", list.Meta.Total, len(list.Data), tt.want)
			}
		})
	}

	t.Run("invalid filters", func(t *testing.T) {
		resp := testutil.Decode[testutil.APIError](t,
			testutil.Do(t, c.api, nil, http.MethodGet, "/products?category_id=abc&active=maybe", ""),
			http.StatusUnprocessableEntity)
		if resp.Error.Fields["category_id"] == "" || resp.Error.Fields["active"] == "" {
			t.Errorf("want errors in category_id and active: %+v", resp.Error)
		}
	})
}

func TestProductsErrors(t *testing.T) {
	c := newCatalog(t)
	c.createProduct(c.drinks, "Café au lait", "cafe-au-lait", true)

	tests := []struct {
		name       string
		cookie     *http.Cookie
		body       string
		wantStatus int
		wantField  string
	}{
		{"no session", nil, `{}`, http.StatusUnauthorized, ""},
		{"regular user", c.user, `{}`, http.StatusForbidden, ""},
		{"repeated slug", c.admin, fmt.Sprintf(`{"category_id":%d,"name":"Other","slug":"cafe-au-lait","price_cents":1}`, c.drinks), http.StatusConflict, "slug"},
		{"missing category", c.admin, `{"category_id":9999,"name":"Other","slug":"other","price_cents":1}`, http.StatusUnprocessableEntity, "category_id"},
		{"negative price", c.admin, fmt.Sprintf(`{"category_id":%d,"name":"Other","slug":"other","price_cents":-1}`, c.drinks), http.StatusUnprocessableEntity, "price_cents"},
		{"no category", c.admin, `{"name":"Other","slug":"other"}`, http.StatusUnprocessableEntity, "category_id"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := testutil.Decode[testutil.APIError](t, testutil.Do(t, c.api, tt.cookie, http.MethodPost, "/products", tt.body), tt.wantStatus)
			if tt.wantField != "" && resp.Error.Fields[tt.wantField] == "" {
				t.Errorf("want an error in %q: %+v", tt.wantField, resp.Error)
			}
		})
	}
}

func TestInactiveProductsOnlyVisibleToAdmin(t *testing.T) {
	c := newCatalog(t)
	c.createProduct(c.drinks, "Café au lait", "cafe-au-lait", true)
	inactive := c.createProduct(c.drinks, "Green tea", "green-tea", false)
	path := fmt.Sprintf("/products/%d", inactive.ID)

	for name, cookie := range map[string]*http.Cookie{"visitor": nil, "regular user": c.user} {
		t.Run(name, func(t *testing.T) {
			// The list only brings active ones, even when it asks for ?active=false.
			for _, query := range []string{"", "?active=false"} {
				list := c.list(cookie, query)
				if list.Meta.Total != 1 || list.Data[0].Name != "Café au lait" {
					t.Errorf("list %q: %+v", query, list)
				}
			}
			// And the inactive product does not exist for them.
			testutil.Decode[testutil.APIError](t, testutil.Do(t, c.api, cookie, http.MethodGet, path, ""), http.StatusNotFound)
		})
	}

	t.Run("admin", func(t *testing.T) {
		if list := c.list(c.admin, ""); list.Meta.Total != 2 {
			t.Errorf("the admin should see the 2 products: %+v", list)
		}
		testutil.Decode[product](t, testutil.Do(t, c.api, c.admin, http.MethodGet, path, ""), http.StatusOK)
	})

	t.Run("an expired or invalid session is treated as a visitor", func(t *testing.T) {
		bad := &http.Cookie{Name: auth.CookieName, Value: "invalid-token"}
		if list := c.list(bad, ""); list.Meta.Total != 1 {
			t.Errorf("unexpected list: %+v", list)
		}
	})
}
