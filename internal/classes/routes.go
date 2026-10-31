package classes

import (
	"net/http"

	"github.com/NoeOnDev/go-api-base/internal/auth"
	"github.com/NoeOnDev/go-api-base/internal/httpx"
)

// Register adds the routes: public reads, writes only for administrators.
func Register(mux *http.ServeMux, h *Handler, authn *auth.Service) {
	mux.Handle("GET /classes", httpx.Handle(h.list))
	mux.Handle("GET /classes/sessions", httpx.Handle(h.agenda))
	mux.Handle("GET /classes/{id}", httpx.Handle(h.get))
	mux.Handle("GET /classes/{id}/sessions", httpx.Handle(h.sessions))

	mux.Handle("POST /classes", authn.RequireAdmin(httpx.Handle(h.create)))
	mux.Handle("PUT /classes/{id}", authn.RequireAdmin(httpx.Handle(h.update)))
	mux.Handle("DELETE /classes/{id}", authn.RequireAdmin(httpx.Handle(h.delete)))
	mux.Handle("PUT /classes/{id}/sessions/{date}", authn.RequireAdmin(httpx.Handle(h.putChange)))
	mux.Handle("DELETE /classes/{id}/sessions/{date}", authn.RequireAdmin(httpx.Handle(h.deleteChange)))
}
