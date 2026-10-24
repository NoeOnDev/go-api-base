package auth

import (
	"net/http"

	"github.com/NoeOnDev/go-api-base/internal/httpx"
)

// Register agrega las rutas de autenticación al mux. limitAttempts se aplica al registro
// y al login, para frenar ataques de fuerza bruta contra las contraseñas.
func Register(mux *http.ServeMux, s *Service, limitAttempts httpx.Middleware) {
	mux.Handle("POST /auth/register", limitAttempts(httpx.Handle(s.register)))
	mux.Handle("POST /auth/login", limitAttempts(httpx.Handle(s.login)))
	mux.Handle("POST /auth/logout", s.RequireUser(httpx.Handle(s.logout)))
	mux.Handle("GET /auth/me", s.RequireUser(httpx.Handle(s.me)))
}
