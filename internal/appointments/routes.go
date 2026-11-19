package appointments

import (
	"net/http"

	"github.com/NoeOnDev/go-api-base/internal/auth"
	"github.com/NoeOnDev/go-api-base/internal/httpx"
)

// Register adds the routes: the availability is public; booking and the appointments, with a session
// (each user sees theirs); a branch's agenda, only for administrators. The ones with appointments
// are not cached, because they are personal data (httpx.NoStore).
func Register(mux *http.ServeMux, h *Handler, authn *auth.Service) {
	mux.Handle("GET /branches/{id}/availability", httpx.Handle(h.availability))
	mux.Handle("GET /branches/{id}/appointments", httpx.NoStore(authn.RequireAdmin(httpx.Handle(h.agenda))))

	handle := func(pattern string, fn httpx.HandlerFunc) {
		mux.Handle(pattern, httpx.NoStore(authn.RequireUser(httpx.Handle(fn))))
	}
	handle("POST /appointments", h.book)
	handle("GET /appointments", h.list)
	handle("GET /appointments/{id}", h.get)
	handle("POST /appointments/{id}/cancel", h.cancel)
}
