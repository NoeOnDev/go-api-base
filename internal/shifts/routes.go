package shifts

import (
	"net/http"

	"github.com/NoeOnDev/go-api-base/internal/auth"
	"github.com/NoeOnDev/go-api-base/internal/httpx"
)

// Register adds the routes: the shifts are assigned by administrators; each user sees theirs and
// records their time. None is cached, because they are each person's data (httpx.NoStore).
func Register(mux *http.ServeMux, h *Handler, authn *auth.Service) {
	admin := func(pattern string, fn httpx.HandlerFunc) {
		mux.Handle(pattern, httpx.NoStore(authn.RequireAdmin(httpx.Handle(fn))))
	}
	user := func(pattern string, fn httpx.HandlerFunc) {
		mux.Handle(pattern, httpx.NoStore(authn.RequireUser(httpx.Handle(fn))))
	}
	admin("POST /shifts", h.create)
	admin("PUT /shifts/{id}", h.update)
	admin("DELETE /shifts/{id}", h.delete)
	user("GET /shifts", h.list)
	user("GET /shifts/{id}", h.get)

	user("POST /time-entries/clock-in", h.clockIn)
	user("POST /time-entries/clock-out", h.clockOut)
	user("GET /time-entries", h.listEntries)
	user("GET /time-entries/summary", h.summary)
}
