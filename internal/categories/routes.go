package categories

import (
	"net/http"

	"github.com/NoeOnDev/go-api-base/internal/auth"
	"github.com/NoeOnDev/go-api-base/internal/httpx"
)

// PermManage is the permission to create, edit and delete categories. The administrators have it,
// and so does any role an administrator gives it to (see "Roles" in docs/auth.md).
const PermManage = "categories.manage"

// Register adds the routes: public reads, writes only for whoever has PermManage.
func Register(mux *http.ServeMux, h *Handler, authn *auth.Service) {
	mux.Handle("GET /categories", httpx.Handle(h.list))
	mux.Handle("GET /categories/{id}", httpx.Handle(h.get))
	mux.Handle("POST /categories", authn.RequirePermission(PermManage, httpx.Handle(h.create)))
	mux.Handle("PUT /categories/{id}", authn.RequirePermission(PermManage, httpx.Handle(h.update)))
	mux.Handle("DELETE /categories/{id}", authn.RequirePermission(PermManage, httpx.Handle(h.delete)))
}
