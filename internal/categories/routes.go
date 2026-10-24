package categories

import (
	"net/http"

	"github.com/NoeOnDev/go-api-base/internal/auth"
	"github.com/NoeOnDev/go-api-base/internal/httpx"
)

// Register agrega las rutas: lectura pública, escritura solo para administradores.
func Register(mux *http.ServeMux, h *Handler, authn *auth.Service) {
	mux.Handle("GET /categories", httpx.Handle(h.list))
	mux.Handle("GET /categories/{id}", httpx.Handle(h.get))
	mux.Handle("POST /categories", authn.RequireAdmin(httpx.Handle(h.create)))
	mux.Handle("PUT /categories/{id}", authn.RequireAdmin(httpx.Handle(h.update)))
	mux.Handle("DELETE /categories/{id}", authn.RequireAdmin(httpx.Handle(h.delete)))
}
