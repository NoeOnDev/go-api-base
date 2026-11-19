package notes

import (
	"net/http"

	"github.com/NoeOnDev/go-api-base/internal/auth"
	"github.com/NoeOnDev/go-api-base/internal/files"
	"github.com/NoeOnDev/go-api-base/internal/httpx"
	"github.com/NoeOnDev/go-api-base/internal/ratelimit"
)

// Register adds the note routes to the mux. All of them require a session and are not cached,
// because they are each user's data (httpx.NoStore). uploads is the limit of uploads per user, the
// same one as POST /files.
func Register(mux *http.ServeMux, h *Handler, authn *auth.Service, uploads *ratelimit.Limiter) {
	handle := func(pattern string, fn httpx.HandlerFunc) {
		mux.Handle(pattern, httpx.NoStore(authn.RequireUser(httpx.Handle(fn))))
	}
	handle("GET /notes", h.list)
	handle("POST /notes", h.create)
	handle("GET /notes/{id}", h.get)
	handle("PUT /notes/{id}", h.update)
	handle("DELETE /notes/{id}", h.delete)

	// Attachments (attachments.go). The files themselves are downloaded from GET /files/{id}.
	handle("GET /notes/{id}/attachments", h.listAttachments)
	mux.Handle("POST /notes/{id}/attachments",
		httpx.NoStore(authn.RequireUser(files.ByUser(uploads)(httpx.Handle(h.attach)))))
	handle("DELETE /notes/{id}/attachments/{file_id}", h.detach)
}
