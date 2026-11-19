package files

import (
	"net/http"
	"strconv"

	"github.com/NoeOnDev/go-api-base/internal/auth"
	"github.com/NoeOnDev/go-api-base/internal/httpx"
	"github.com/NoeOnDev/go-api-base/internal/ratelimit"
)

// Register adds the file routes to the mux. All of them require a session. uploads limits how many
// files a user uploads (see ByUser); a resource with its own upload route puts it there too.
func Register(mux *http.ServeMux, s *Service, authn *auth.Service, uploads *ratelimit.Limiter) {
	handle := func(pattern string, h http.Handler) {
		mux.Handle(pattern, httpx.NoStore(authn.RequireUser(h)))
	}
	handle("POST /files", ByUser(uploads)(httpx.Handle(s.upload)))
	// A file served sets its own Cache-Control (see Service.Serve); no-store is for the errors.
	handle("GET /files/{id}", httpx.Handle(s.download))
	handle("DELETE /files/{id}", httpx.Handle(s.remove))
}

// ByUser limits a route by the session's user: it goes inside RequireUser.
func ByUser(l *ratelimit.Limiter) httpx.Middleware {
	return l.Middleware(func(r *http.Request) string {
		return strconv.FormatInt(auth.MustUser(r.Context()).ID, 10)
	})
}
