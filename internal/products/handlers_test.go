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

// catalog prepara la base con un admin, un usuario común y dos categorías.
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
	_, c.admin = testutil.Login(t, pool, "admin@ejemplo.com", auth.RoleAdmin)
	_, c.user = testutil.Login(t, pool, "user@ejemplo.com", auth.RoleUser)
	c.drinks = c.createCategory("Bebidas", "bebidas")
	c.food = c.createCategory("Comida", "comida")
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

// list pide el listado con la sesión indicada (nil = visitante anónimo).
func (c *catalog) list(cookie *http.Cookie, query string) productList {
	c.t.Helper()
	return testutil.Decode[productList](c.t, testutil.Do(c.t, c.api, cookie, http.MethodGet, "/products"+query, ""), http.StatusOK)
}

func TestProductsCRUD(t *testing.T) {
	c := newCatalog(t)

	// active no se envía: por defecto es true. La respuesta incluye la categoría (JOIN).
	body := fmt.Sprintf(`{"category_id":%d,"name":"Café molido","slug":"cafe-molido","price_cents":1250,"stock":5}`, c.drinks)
	p := testutil.Decode[product](t, testutil.Do(t, c.api, c.admin, http.MethodPost, "/products", body), http.StatusCreated)
	if p.Category.ID != c.drinks || p.Category.Name != "Bebidas" || !p.Active || p.PriceCents != 1250 {
		t.Fatalf("producto inesperado: %+v", p)
	}
	path := fmt.Sprintf("/products/%d", p.ID)

	// Lectura pública.
	got := testutil.Decode[product](t, testutil.Do(t, c.api, nil, http.MethodGet, path, ""), http.StatusOK)
	if got != p {
		t.Errorf("GET = %+v, se esperaba %+v", got, p)
	}

	// Editar: cambia de categoría y se desactiva.
	body = fmt.Sprintf(`{"category_id":%d,"name":"Café en grano","slug":"cafe-grano","price_cents":1800,"stock":0,"active":false}`, c.food)
	updated := testutil.Decode[product](t, testutil.Do(t, c.api, c.admin, http.MethodPut, path, body), http.StatusOK)
	if updated.Category.Name != "Comida" || updated.Active || updated.Slug != "cafe-grano" {
		t.Errorf("producto editado inesperado: %+v", updated)
	}

	if w := testutil.Do(t, c.api, c.admin, http.MethodDelete, path, ""); w.Code != http.StatusNoContent {
		t.Fatalf("DELETE status = %d", w.Code)
	}
	testutil.Decode[testutil.APIError](t, testutil.Do(t, c.api, nil, http.MethodGet, path, ""), http.StatusNotFound)
}

// Los filtros se prueban como admin, que ve también los productos inactivos.
func TestProductsFilters(t *testing.T) {
	c := newCatalog(t)
	c.createProduct(c.drinks, "Café molido", "cafe-molido", true)
	c.createProduct(c.drinks, "Té verde", "te-verde", false)
	c.createProduct(c.food, "Pan de CAFÉ", "pan-cafe", true)
	c.createProduct(c.food, "Descuento 50%", "descuento-50", true)

	tests := []struct {
		name  string
		query string
		want  int64
	}{
		{"sin filtros", "", 4},
		{"por categoría", fmt.Sprintf("?category_id=%d", c.drinks), 2},
		{"búsqueda sin distinguir mayúsculas", "?q=" + url.QueryEscape("café"), 2},
		{"solo activos", "?active=true", 3},
		{"filtros combinados", fmt.Sprintf("?category_id=%d&active=true", c.drinks), 1},
		{"el % se busca como texto", "?q=" + url.QueryEscape("50%"), 1},
		{"el % no es comodín", "?q=" + url.QueryEscape("%"), 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			list := c.list(c.admin, tt.query)
			if list.Meta.Total != tt.want || int64(len(list.Data)) != tt.want {
				t.Errorf("total = %d, items = %d, se esperaba %d", list.Meta.Total, len(list.Data), tt.want)
			}
		})
	}

	t.Run("filtros inválidos", func(t *testing.T) {
		resp := testutil.Decode[testutil.APIError](t,
			testutil.Do(t, c.api, nil, http.MethodGet, "/products?category_id=abc&active=quizas", ""),
			http.StatusUnprocessableEntity)
		if resp.Error.Fields["category_id"] == "" || resp.Error.Fields["active"] == "" {
			t.Errorf("se esperaban errores en category_id y active: %+v", resp.Error)
		}
	})
}

func TestProductsErrors(t *testing.T) {
	c := newCatalog(t)
	c.createProduct(c.drinks, "Café molido", "cafe-molido", true)

	tests := []struct {
		name       string
		cookie     *http.Cookie
		body       string
		wantStatus int
		wantField  string
	}{
		{"sin sesión", nil, `{}`, http.StatusUnauthorized, ""},
		{"usuario común", c.user, `{}`, http.StatusForbidden, ""},
		{"slug repetido", c.admin, fmt.Sprintf(`{"category_id":%d,"name":"Otro","slug":"cafe-molido","price_cents":1}`, c.drinks), http.StatusConflict, "slug"},
		{"categoría inexistente", c.admin, `{"category_id":9999,"name":"Otro","slug":"otro","price_cents":1}`, http.StatusUnprocessableEntity, "category_id"},
		{"precio negativo", c.admin, fmt.Sprintf(`{"category_id":%d,"name":"Otro","slug":"otro","price_cents":-1}`, c.drinks), http.StatusUnprocessableEntity, "price_cents"},
		{"sin categoría", c.admin, `{"name":"Otro","slug":"otro"}`, http.StatusUnprocessableEntity, "category_id"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := testutil.Decode[testutil.APIError](t, testutil.Do(t, c.api, tt.cookie, http.MethodPost, "/products", tt.body), tt.wantStatus)
			if tt.wantField != "" && resp.Error.Fields[tt.wantField] == "" {
				t.Errorf("se esperaba error en %q: %+v", tt.wantField, resp.Error)
			}
		})
	}
}

func TestInactiveProductsOnlyVisibleToAdmin(t *testing.T) {
	c := newCatalog(t)
	c.createProduct(c.drinks, "Café molido", "cafe-molido", true)
	inactive := c.createProduct(c.drinks, "Té verde", "te-verde", false)
	path := fmt.Sprintf("/products/%d", inactive.ID)

	for name, cookie := range map[string]*http.Cookie{"visitante": nil, "usuario común": c.user} {
		t.Run(name, func(t *testing.T) {
			// El listado solo trae activos, aunque pida ?active=false.
			for _, query := range []string{"", "?active=false"} {
				list := c.list(cookie, query)
				if list.Meta.Total != 1 || list.Data[0].Name != "Café molido" {
					t.Errorf("listado %q: %+v", query, list)
				}
			}
			// Y el producto inactivo no existe para él.
			testutil.Decode[testutil.APIError](t, testutil.Do(t, c.api, cookie, http.MethodGet, path, ""), http.StatusNotFound)
		})
	}

	t.Run("admin", func(t *testing.T) {
		if list := c.list(c.admin, ""); list.Meta.Total != 2 {
			t.Errorf("el admin debería ver los 2 productos: %+v", list)
		}
		testutil.Decode[product](t, testutil.Do(t, c.api, c.admin, http.MethodGet, path, ""), http.StatusOK)
	})

	t.Run("sesión vencida o inválida se trata como visitante", func(t *testing.T) {
		bad := &http.Cookie{Name: auth.CookieName, Value: "token-invalido"}
		if list := c.list(bad, ""); list.Meta.Total != 1 {
			t.Errorf("listado inesperado: %+v", list)
		}
	})
}
