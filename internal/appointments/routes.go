package appointments

import (
	"net/http"

	"github.com/NoeOnDev/go-api-base/internal/auth"
	"github.com/NoeOnDev/go-api-base/internal/httpx"
)

// Register adds the routes: the availability is public; booking and the appointments, with a session
// (each user sees theirs); a branch's agenda, only for administrators.
func Register(mux *http.ServeMux, h *Handler, authn *auth.Service) {
	mux.Handle("GET /branches/{id}/availability", httpx.Handle(h.availability))
	mux.Handle("GET /branches/{id}/appointments", authn.RequireAdmin(httpx.Handle(h.agenda)))

	mux.Handle("POST /appointments", authn.RequireUser(httpx.Handle(h.book)))
	mux.Handle("GET /appointments", authn.RequireUser(httpx.Handle(h.list)))
	mux.Handle("GET /appointments/{id}", authn.RequireUser(httpx.Handle(h.get)))
	mux.Handle("POST /appointments/{id}/cancel", authn.RequireUser(httpx.Handle(h.cancel)))
}
