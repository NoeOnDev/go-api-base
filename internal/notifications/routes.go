package notifications

import (
	"net/http"

	"github.com/NoeOnDev/go-api-base/internal/auth"
	"github.com/NoeOnDev/go-api-base/internal/httpx"
)

// Register adds the notification routes to the mux. All of them require a session and are not
// cached, because they are personal data.
func Register(mux *http.ServeMux, s *Service, authn *auth.Service) {
	handle := func(pattern string, h httpx.HandlerFunc) {
		mux.Handle(pattern, httpx.NoStore(authn.RequireUser(httpx.Handle(h))))
	}
	handle("GET /notifications", s.list)
	handle("POST /notifications/{id}/read", s.markRead)
	handle("POST /notifications/read-all", s.markAllRead)
}
