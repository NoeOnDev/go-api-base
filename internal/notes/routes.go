package notes

import (
	"net/http"

	"github.com/NoeOnDev/go-api-base/internal/auth"
	"github.com/NoeOnDev/go-api-base/internal/httpx"
)

// Register agrega las rutas de notas al mux. Todas requieren una sesión.
func Register(mux *http.ServeMux, h *Handler, authn *auth.Service) {
	mux.Handle("GET /notes", authn.RequireUser(httpx.Handle(h.list)))
	mux.Handle("POST /notes", authn.RequireUser(httpx.Handle(h.create)))
	mux.Handle("GET /notes/{id}", authn.RequireUser(httpx.Handle(h.get)))
	mux.Handle("PUT /notes/{id}", authn.RequireUser(httpx.Handle(h.update)))
	mux.Handle("DELETE /notes/{id}", authn.RequireUser(httpx.Handle(h.delete)))
}
