package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/NoeOnDev/go-api-base/internal/store"
)

// CookieName es el nombre de la cookie que lleva el token de sesión.
const CookieName = "session"

// errNoSession indica que la petición no trae una sesión válida.
var errNoSession = errors.New("sin sesión válida")

// newSessionToken genera un token aleatorio de 32 bytes (256 bits) para el cliente
// y el hash que se guarda en la base.
func newSessionToken() (token string, hash []byte, err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", nil, fmt.Errorf("generar token: %w", err)
	}
	token = base64.RawURLEncoding.EncodeToString(b)
	return token, hashToken(token), nil
}

// hashToken devuelve el SHA-256 del token. Alcanza con un hash rápido (no argon2) porque
// el token ya es aleatorio y largo: no se puede adivinar por fuerza bruta.
func hashToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

// StartSession crea una sesión para el usuario y envía la cookie.
// De paso borra las sesiones vencidas, para que la tabla no crezca sin límite.
func (s *Service) StartSession(ctx context.Context, w http.ResponseWriter, userID int64) error {
	token, hash, err := newSessionToken()
	if err != nil {
		return err
	}

	expires := time.Now().Add(s.opts.SessionTTL)
	if err := s.q.CreateSession(ctx, store.CreateSessionParams{
		TokenHash: hash,
		UserID:    userID,
		ExpiresAt: expires,
	}); err != nil {
		return fmt.Errorf("crear sesión: %w", err)
	}

	if _, err := s.q.DeleteExpiredSessions(ctx); err != nil {
		return fmt.Errorf("borrar sesiones vencidas: %w", err)
	}

	http.SetCookie(w, s.cookie(token, expires))
	return nil
}

// EndSession borra la sesión de la petición (si hay) y le indica al navegador que borre la cookie.
func (s *Service) EndSession(ctx context.Context, w http.ResponseWriter, r *http.Request) error {
	if c, err := r.Cookie(CookieName); err == nil {
		if err := s.q.DeleteSession(ctx, hashToken(c.Value)); err != nil {
			return fmt.Errorf("borrar sesión: %w", err)
		}
	}

	expired := s.cookie("", time.Unix(0, 0))
	expired.MaxAge = -1
	http.SetCookie(w, expired)
	return nil
}

// userFromRequest busca el usuario de la sesión enviada en la cookie.
// Devuelve errNoSession si no hay cookie, la sesión no existe o venció.
func (s *Service) userFromRequest(r *http.Request) (User, error) {
	c, err := r.Cookie(CookieName)
	if err != nil {
		return User{}, errNoSession
	}

	u, err := s.q.GetSessionUser(r.Context(), hashToken(c.Value))
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, errNoSession
	}
	if err != nil {
		return User{}, fmt.Errorf("buscar sesión: %w", err)
	}
	return toUser(u), nil
}

// cookie arma la cookie de sesión:
//   - HttpOnly: JavaScript no puede leerla, así que un ataque XSS no puede robar la sesión.
//   - SameSite=Lax: el navegador no la envía en peticiones que otro sitio dispara
//     (por ejemplo un formulario oculto), la primera barrera contra CSRF.
//   - Secure: solo viaja por HTTPS (se desactiva en desarrollo, donde se usa http://localhost).
func (s *Service) cookie(value string, expires time.Time) *http.Cookie {
	return &http.Cookie{
		Name:     CookieName,
		Value:    value,
		Path:     "/",
		Expires:  expires,
		HttpOnly: true,
		Secure:   s.opts.CookieSecure,
		SameSite: http.SameSiteLaxMode,
	}
}
