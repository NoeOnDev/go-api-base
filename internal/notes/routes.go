package notes

import (
	"net/http"

	"github.com/NoeOnDev/go-api-base/internal/auth"
	"github.com/NoeOnDev/go-api-base/internal/httpx"
)

// Register adds the note routes to the mux. All of them require a session and are not cached,
// because they are each user's data (httpx.NoStore).
func Register(mux *http.ServeMux, h *Handler, authn *auth.Service) {
	handle := func(pattern string, fn httpx.HandlerFunc) {
		mux.Handle(pattern, httpx.NoStore(authn.RequireUser(httpx.Handle(fn))))
	}
	handle("GET /notes", h.list)
	handle("POST /notes", h.create)
	handle("GET /notes/{id}", h.get)
	handle("PUT /notes/{id}", h.update)
	handle("DELETE /notes/{id}", h.delete)
}
