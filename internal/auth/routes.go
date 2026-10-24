package auth

import (
	"net/http"

	"github.com/NoeOnDev/go-api-base/internal/httpx"
)

// Limits son los límites de peticiones que se aplican a las rutas sensibles.
type Limits struct {
	// Attempts frena la fuerza bruta contra contraseñas y tokens (login, registro, cambio y reset).
	Attempts httpx.Middleware
	// Emails frena el abuso de las rutas que envían correos.
	Emails httpx.Middleware
}

// Register agrega las rutas de autenticación al mux.
//
// Para quitar una función que un proyecto no necesita, borrá sus rutas acá
// (y su archivo: password_reset.go, profile.go…). docs/auth.md lo detalla.
func Register(mux *http.ServeMux, s *Service, limits Limits) {
	// Registro e inicio de sesión.
	mux.Handle("POST /auth/register", limits.Attempts(httpx.Handle(s.register)))
	mux.Handle("POST /auth/login", limits.Attempts(httpx.Handle(s.login)))
	// El logout no exige una sesión válida: si venció, igual tiene que borrar la cookie del navegador.
	mux.Handle("POST /auth/logout", httpx.Handle(s.logout))

	// Cuenta del usuario.
	mux.Handle("GET /auth/me", s.requireSession(httpx.Handle(s.me)))
	mux.Handle("PUT /auth/me", s.requireSession(httpx.Handle(s.updateProfile)))
	mux.Handle("PUT /auth/password", limits.Attempts(s.RequireUser(httpx.Handle(s.changePassword))))

	// Recuperar la contraseña por correo.
	mux.Handle("POST /auth/password/forgot", limits.Emails(httpx.Handle(s.forgotPassword)))
	mux.Handle("POST /auth/password/reset", limits.Attempts(httpx.Handle(s.resetPassword)))

	// Verificar el email (obligatorio solo con REQUIRE_EMAIL_VERIFICATION=true).
	mux.Handle("POST /auth/email/verify", limits.Attempts(httpx.Handle(s.verifyEmail)))
	mux.Handle("POST /auth/email/resend", limits.Emails(s.requireSession(httpx.Handle(s.resendVerification))))
}
