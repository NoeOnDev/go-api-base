package notes

import (
	"net/http"

	"github.com/NoeOnDev/go-api-base/internal/httpx"
)

// Register agrega las rutas de notas al mux.
func Register(mux *http.ServeMux, h *Handler) {
	mux.Handle("GET /notes", httpx.Handle(h.list))
	mux.Handle("POST /notes", httpx.Handle(h.create))
	mux.Handle("GET /notes/{id}", httpx.Handle(h.get))
	mux.Handle("PUT /notes/{id}", httpx.Handle(h.update))
	mux.Handle("DELETE /notes/{id}", httpx.Handle(h.delete))
}
