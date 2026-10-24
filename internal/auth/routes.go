package auth

import (
	"net/http"

	"github.com/NoeOnDev/go-api-base/internal/httpx"
)

// Register agrega las rutas de autenticación al mux.
func Register(mux *http.ServeMux, s *Service) {
	mux.Handle("POST /auth/register", httpx.Handle(s.register))
	mux.Handle("POST /auth/login", httpx.Handle(s.login))
	mux.Handle("POST /auth/logout", s.RequireUser(httpx.Handle(s.logout)))
	mux.Handle("GET /auth/me", s.RequireUser(httpx.Handle(s.me)))
}
