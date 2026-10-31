package branches

import (
	"net/http"

	"github.com/NoeOnDev/go-api-base/internal/auth"
	"github.com/NoeOnDev/go-api-base/internal/httpx"
)

// Register adds the routes: public reads, writes only for administrators.
func Register(mux *http.ServeMux, h *Handler, authn *auth.Service) {
	mux.Handle("GET /branches", httpx.Handle(h.list))
	mux.Handle("GET /branches/{id}", httpx.Handle(h.get))
	mux.Handle("GET /branches/{id}/schedule", httpx.Handle(h.schedule))
	mux.Handle("GET /branches/{id}/status", httpx.Handle(h.status))
	mux.Handle("GET /branches/{id}/exceptions", httpx.Handle(h.listExceptions))

	mux.Handle("POST /branches", authn.RequireAdmin(httpx.Handle(h.create)))
	mux.Handle("PUT /branches/{id}", authn.RequireAdmin(httpx.Handle(h.update)))
	mux.Handle("DELETE /branches/{id}", authn.RequireAdmin(httpx.Handle(h.delete)))
	mux.Handle("PUT /branches/{id}/hours", authn.RequireAdmin(httpx.Handle(h.replaceHours)))
	mux.Handle("PUT /branches/{id}/exceptions/{date}", authn.RequireAdmin(httpx.Handle(h.putException)))
	mux.Handle("DELETE /branches/{id}/exceptions/{date}", authn.RequireAdmin(httpx.Handle(h.deleteException)))
}
