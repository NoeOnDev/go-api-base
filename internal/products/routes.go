package products

import (
	"net/http"

	"github.com/NoeOnDev/go-api-base/internal/auth"
	"github.com/NoeOnDev/go-api-base/internal/httpx"
)

// PermManage is the permission to create, edit and delete products, and to see the inactive ones.
// The administrators have it, and so does any role an administrator gives it to (see "Roles" in
// docs/auth.md).
const PermManage = "products.manage"

// Register adds the routes: public reads (with the inactive products for whoever manages them),
// writes only for whoever has PermManage.
func Register(mux *http.ServeMux, h *Handler, authn *auth.Service) {
	mux.Handle("GET /products", authn.OptionalUser(httpx.Handle(h.list)))
	mux.Handle("GET /products/{id}", authn.OptionalUser(httpx.Handle(h.get)))
	mux.Handle("POST /products", authn.RequirePermission(PermManage, httpx.Handle(h.create)))
	mux.Handle("PUT /products/{id}", authn.RequirePermission(PermManage, httpx.Handle(h.update)))
	mux.Handle("DELETE /products/{id}", authn.RequirePermission(PermManage, httpx.Handle(h.delete)))
}
