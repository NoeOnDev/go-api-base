package auth

import (
	"context"
	"errors"
	"net/http"

	"github.com/NoeOnDev/go-api-base/internal/httpx"
)

type ctxKey struct{}

// sessionIDKey guarda en el contexto el id de la sesión de la petición (ver currentSessionID).
type sessionIDKey struct{}

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

var errEmailNotVerified = &httpx.Error{Status: http.StatusForbidden, Code: "email_not_verified"}

// requireSession exige una sesión válida, pero no el email verificado. Lo usan las rutas
// de la propia cuenta (ver el perfil, cerrar sesión, reenviar la verificación), que tienen
// que funcionar aunque el usuario todavía no haya confirmado su email.
func (s *Service) requireSession(next http.Handler) http.Handler {
	return httpx.Handle(func(w http.ResponseWriter, r *http.Request) error {
		sess, err := s.sessionFromRequest(r)
		if errors.Is(err, errNoSession) {
			return httpx.Unauthorized("unauthorized")
		}
		if err != nil {
			return err
		}
		s.touch(r, sess)

		// Desde acá, cada línea de log de la petición (incluida la final) incluye el usuario.
		httpx.AddLogAttrs(r.Context(), "user_id", sess.user.ID)

		ctx := context.WithValue(WithUser(r.Context(), sess.user), sessionIDKey{}, sess.id)
		next.ServeHTTP(w, r.WithContext(ctx))
		return nil
	})
}

// OptionalUser identifica al usuario si la petición trae una sesión válida, pero no la exige:
// sin sesión (o con una vencida) la petición sigue como anónima. Sirve para rutas públicas que
// muestran más datos a ciertos usuarios; en el handler se consulta con UserFrom.
func (s *Service) OptionalUser(next http.Handler) http.Handler {
	return httpx.Handle(func(w http.ResponseWriter, r *http.Request) error {
		sess, err := s.sessionFromRequest(r)
		if errors.Is(err, errNoSession) {
			next.ServeHTTP(w, r)
			return nil
		}
		if err != nil {
			return err
		}
		s.touch(r, sess)

		httpx.AddLogAttrs(r.Context(), "user_id", sess.user.ID)
		ctx := context.WithValue(WithUser(r.Context(), sess.user), sessionIDKey{}, sess.id)
		next.ServeHTTP(w, r.WithContext(ctx))
		return nil
	})
}

// RequireAdmin deja pasar solo a usuarios con rol admin: 401 sin sesión, 403 con otro rol.
func (s *Service) RequireAdmin(next http.Handler) http.Handler {
	return s.RequireUser(httpx.Handle(func(w http.ResponseWriter, r *http.Request) error {
		if !MustUser(r.Context()).IsAdmin() {
			return httpx.Forbidden("forbidden")
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
