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
	// El logout no exige una sesión válida: si venció, igual tiene que borrar la cookie del navegador.
	mux.Handle("POST /auth/logout", httpx.Handle(s.logout))
	mux.Handle("GET /auth/me", s.requireSession(httpx.Handle(s.me)))
	mux.Handle("PUT /auth/me", s.requireSession(httpx.Handle(s.updateProfile)))
	mux.Handle("PUT /auth/password", limitAttempts(s.RequireUser(httpx.Handle(s.changePassword))))
}
