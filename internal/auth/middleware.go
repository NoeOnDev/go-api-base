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
func (s *Service) RequireUser(next http.Handler) http.Handler {
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

		ctx := context.WithValue(r.Context(), ctxKey{}, u)
		next.ServeHTTP(w, r.WithContext(ctx))
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
