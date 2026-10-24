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
	// Ninguna respuesta de /auth se guarda en caché: algunas llevan secretos (el del 2FA,
	// los códigos de recuperación) y las demás, datos de la cuenta. NoStore va por fuera
	// de los límites para que también lo lleven los 429.
	handle := func(pattern string, h http.Handler) { mux.Handle(pattern, httpx.NoStore(h)) }

	// Registro e inicio de sesión.
	handle("POST /auth/register", limits.Attempts(httpx.Handle(s.register)))
	handle("POST /auth/login", limits.Attempts(httpx.Handle(s.login)))
	// El logout no exige una sesión válida: si venció, igual tiene que borrar la cookie del navegador.
	handle("POST /auth/logout", httpx.Handle(s.logout))

	// Cuenta del usuario.
	handle("GET /auth/me", s.requireSession(httpx.Handle(s.me)))
	handle("PUT /auth/me", s.requireSession(httpx.Handle(s.updateProfile)))
	handle("PUT /auth/password", limits.Attempts(s.RequireUser(httpx.Handle(s.changePassword))))

	// Recuperar la contraseña por correo.
	handle("POST /auth/password/forgot", limits.Emails(httpx.Handle(s.forgotPassword)))
	handle("POST /auth/password/reset", limits.Attempts(httpx.Handle(s.resetPassword)))

	// 2FA con app de autenticación (voluntario para cada usuario).
	handle("POST /auth/two-factor/challenge", limits.Attempts(httpx.Handle(s.twoFactorChallenge)))
	handle("POST /auth/two-factor/setup", s.RequireUser(httpx.Handle(s.setupTwoFactor)))
	handle("POST /auth/two-factor/enable", limits.Attempts(s.RequireUser(httpx.Handle(s.enableTwoFactor))))
	handle("POST /auth/two-factor/disable", limits.Attempts(s.RequireUser(httpx.Handle(s.disableTwoFactor))))
	handle("POST /auth/two-factor/recovery-codes", limits.Attempts(s.RequireUser(httpx.Handle(s.regenerateRecoveryCodes))))

	// Verificar el email (obligatorio solo con REQUIRE_EMAIL_VERIFICATION=true).
	handle("POST /auth/email/verify", limits.Attempts(httpx.Handle(s.verifyEmail)))
	handle("POST /auth/email/resend", limits.Emails(s.requireSession(httpx.Handle(s.resendVerification))))
}
