package shifts

import (
	"net/http"

	"github.com/NoeOnDev/go-api-base/internal/auth"
	"github.com/NoeOnDev/go-api-base/internal/httpx"
)

// Register adds the routes: the shifts are assigned by administrators; each user sees theirs and
// records their time.
func Register(mux *http.ServeMux, h *Handler, authn *auth.Service) {
	mux.Handle("POST /shifts", authn.RequireAdmin(httpx.Handle(h.create)))
	mux.Handle("PUT /shifts/{id}", authn.RequireAdmin(httpx.Handle(h.update)))
	mux.Handle("DELETE /shifts/{id}", authn.RequireAdmin(httpx.Handle(h.delete)))
	mux.Handle("GET /shifts", authn.RequireUser(httpx.Handle(h.list)))
	mux.Handle("GET /shifts/{id}", authn.RequireUser(httpx.Handle(h.get)))

	mux.Handle("POST /time-entries/clock-in", authn.RequireUser(httpx.Handle(h.clockIn)))
	mux.Handle("POST /time-entries/clock-out", authn.RequireUser(httpx.Handle(h.clockOut)))
	mux.Handle("GET /time-entries", authn.RequireUser(httpx.Handle(h.listEntries)))
	mux.Handle("GET /time-entries/summary", authn.RequireUser(httpx.Handle(h.summary)))
}
