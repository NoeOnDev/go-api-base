package products

import (
	"net/http"

	"github.com/NoeOnDev/go-api-base/internal/auth"
	"github.com/NoeOnDev/go-api-base/internal/httpx"
)

// Register agrega las rutas: lectura pública (el admin ve también los inactivos),
// escritura solo para administradores.
func Register(mux *http.ServeMux, h *Handler, authn *auth.Service) {
	mux.Handle("GET /products", authn.OptionalUser(httpx.Handle(h.list)))
	mux.Handle("GET /products/{id}", authn.OptionalUser(httpx.Handle(h.get)))
	mux.Handle("POST /products", authn.RequireAdmin(httpx.Handle(h.create)))
	mux.Handle("PUT /products/{id}", authn.RequireAdmin(httpx.Handle(h.update)))
	mux.Handle("DELETE /products/{id}", authn.RequireAdmin(httpx.Handle(h.delete)))
}
