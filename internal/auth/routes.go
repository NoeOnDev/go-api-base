package auth

import (
	"net/http"
	"strconv"

	"github.com/NoeOnDev/go-api-base/internal/httpx"
)

// Limits son los límites de peticiones de las rutas de auth, uno por grupo, así un grupo no
// gasta los intentos de otro (por ejemplo, el segundo paso del 2FA no gasta los del login).
// Los valores se eligen en server/routes.go.
type Limits struct {
	// Login, Register y TwoFactor (el segundo paso del login) se limitan por IP.
	Login, Register, TwoFactor httpx.Middleware
	// Tokens: las rutas que reciben un token de un correo (recuperar la contraseña, verificar
	// o confirmar el email). Por IP.
	Tokens httpx.Middleware
	// Emails: las rutas que envían correos. Por IP; además hay un límite por destinatario.
	Emails httpx.Middleware
	// Account: las acciones sensibles con sesión (cambiar la contraseña, el 2FA…). Se limita
	// por usuario y no por IP: el ataque lo haría alguien con una sesión, y así los usuarios
	// detrás de una misma IP (una oficina, datos móviles) no comparten el límite.
	Account *httpx.RateLimiter
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

	// account limita por usuario: va dentro de RequireUser o requireSession, que ponen el usuario.
	account := limits.Account.MiddlewareBy(func(r *http.Request) string {
		return "account:" + strconv.FormatInt(MustUser(r.Context()).ID, 10)
	})

	// Registro e inicio de sesión.
	handle("POST /auth/register", limits.Register(httpx.Handle(s.register)))
	handle("POST /auth/login", limits.Login(httpx.Handle(s.login)))
	// El logout no exige una sesión válida: si venció, igual tiene que borrar la cookie del navegador.
	handle("POST /auth/logout", httpx.Handle(s.logout))

	// Cuenta del usuario.
	handle("GET /auth/me", s.requireSession(httpx.Handle(s.me)))
	handle("PUT /auth/me", s.requireSession(httpx.Handle(s.updateProfile)))
	handle("PUT /auth/password", s.RequireUser(account(httpx.Handle(s.changePassword))))

	// Recuperar la contraseña por correo.
	handle("POST /auth/password/forgot", limits.Emails(httpx.Handle(s.forgotPassword)))
	handle("POST /auth/password/reset", limits.Tokens(httpx.Handle(s.resetPassword)))

	// 2FA con app de autenticación (voluntario para cada usuario).
	handle("POST /auth/two-factor/challenge", limits.TwoFactor(httpx.Handle(s.twoFactorChallenge)))
	handle("POST /auth/two-factor/setup", s.RequireUser(httpx.Handle(s.setupTwoFactor)))
	handle("POST /auth/two-factor/enable", s.RequireUser(account(httpx.Handle(s.enableTwoFactor))))
	handle("POST /auth/two-factor/disable", s.RequireUser(account(httpx.Handle(s.disableTwoFactor))))
	handle("POST /auth/two-factor/recovery-codes", s.RequireUser(account(httpx.Handle(s.regenerateRecoveryCodes))))

	// Verificar el email (obligatorio solo con REQUIRE_EMAIL_VERIFICATION=true).
	handle("POST /auth/email/verify", limits.Tokens(httpx.Handle(s.verifyEmail)))
	handle("POST /auth/email/resend", limits.Emails(s.requireSession(httpx.Handle(s.resendVerification))))
}
