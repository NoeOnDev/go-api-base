package auth

import (
	"context"
	"errors"
	"net/http"

	"github.com/NoeOnDev/go-api-base/internal/httpx"
)

type ctxKey struct{}

// RequireUser deja pasar solo peticiones con una sesión válida (si no, responde 401)
// y guarda el usuario en el contexto. Los handlers lo obtienen con UserFrom o MustUser.
//
// Con REQUIRE_EMAIL_VERIFICATION activa, además exige el email verificado: si no lo está,
// responde 403 con el código "email_not_verified", para que el frontend muestre el aviso.
func (s *Service) RequireUser(next http.Handler) http.Handler {
	if !s.opts.RequireVerifiedEmail {
		return s.requireSession(next)
	}
	return s.requireSession(httpx.Handle(func(w http.ResponseWriter, r *http.Request) error {
		if !MustUser(r.Context()).EmailVerified() {
			return errEmailNotVerified
		}
		next.ServeHTTP(w, r)
		return nil
	}))
}

var errEmailNotVerified = &httpx.Error{
	Status:  http.StatusForbidden,
	Code:    "email_not_verified",
	Message: "confirmá tu email para continuar; revisá tu correo o pedí otro enlace",
}

// requireSession exige una sesión válida, pero no el email verificado. Lo usan las rutas
// de la propia cuenta (ver el perfil, cerrar sesión, reenviar la verificación), que tienen
// que funcionar aunque el usuario todavía no haya confirmado su email.
func (s *Service) requireSession(next http.Handler) http.Handler {
	return httpx.Handle(func(w http.ResponseWriter, r *http.Request) error {
		u, err := s.userFromRequest(r)
		if errors.Is(err, errNoSession) {
			return httpx.Unauthorized("necesitás iniciar sesión")
		}
		if err != nil {
			return err
		}

		// Desde acá, cada línea de log de la petición (incluida la final) incluye el usuario.
		httpx.AddLogAttrs(r.Context(), "user_id", u.ID)

		ctx := WithUser(r.Context(), u)
		next.ServeHTTP(w, r.WithContext(ctx))
		return nil
	})
}

// OptionalUser identifica al usuario si la petición trae una sesión válida, pero no la exige:
// sin sesión (o con una vencida) la petición sigue como anónima. Sirve para rutas públicas que
// muestran más datos a ciertos usuarios; en el handler se consulta con UserFrom.
func (s *Service) OptionalUser(next http.Handler) http.Handler {
	return httpx.Handle(func(w http.ResponseWriter, r *http.Request) error {
		u, err := s.userFromRequest(r)
		if errors.Is(err, errNoSession) {
			next.ServeHTTP(w, r)
			return nil
		}
		if err != nil {
			return err
		}

		httpx.AddLogAttrs(r.Context(), "user_id", u.ID)
		next.ServeHTTP(w, r.WithContext(WithUser(r.Context(), u)))
		return nil
	})
}

// RequireAdmin deja pasar solo a usuarios con rol admin: 401 sin sesión, 403 con otro rol.
func (s *Service) RequireAdmin(next http.Handler) http.Handler {
	return s.RequireUser(httpx.Handle(func(w http.ResponseWriter, r *http.Request) error {
		if !MustUser(r.Context()).IsAdmin() {
			return httpx.Forbidden("solo un administrador puede hacer esto")
		}
		next.ServeHTTP(w, r)
		return nil
	}))
}

// WithUser devuelve un contexto con el usuario autenticado. Lo usan los middlewares; también
// sirve para probar un handler protegido sin pasar por el login.
func WithUser(ctx context.Context, u User) context.Context {
	return context.WithValue(ctx, ctxKey{}, u)
}

// UserFrom devuelve el usuario autenticado de la petición, si lo hay.
func UserFrom(ctx context.Context) (User, bool) {
	u, ok := ctx.Value(ctxKey{}).(User)
	return u, ok
}

// MustUser devuelve el usuario autenticado. Usalo solo en handlers protegidos con
// RequireUser o RequireAdmin: si no hay usuario es un error de programación y entra en pánico
// (el middleware Recover responde 500 y lo registra).
func MustUser(ctx context.Context) User {
	u, ok := UserFrom(ctx)
	if !ok {
		panic("auth.MustUser: la ruta no está protegida con RequireUser")
	}
	return u
}
