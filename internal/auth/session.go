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
// Con CookieSecure se le agrega el prefijo __Host- (ver cookieName).
const CookieName = "session"

// ChallengeCookieName es la cookie del desafío del 2FA: el login con la contraseña correcta
// la envía, y POST /auth/two-factor/challenge la usa para saber qué usuario está entrando.
// Con CookieSecure también lleva el prefijo __Host-.
const ChallengeCookieName = "two_factor_challenge"

// hostPrefix hace que el navegador solo acepte la cookie si es Secure, tiene Path=/ y no
// tiene Domain. Así ningún subdominio (por ejemplo, uno comprometido) puede crearla ni pisarla.
const hostPrefix = "__Host-"

// cookieName devuelve el nombre real de una cookie: con el prefijo __Host- cuando las cookies
// son Secure (producción). En desarrollo no se puede usar, porque exige HTTPS.
func (s *Service) cookieName(base string) string {
	if s.opts.CookieSecure {
		return hostPrefix + base
	}
	return base
}

// errNoSession indica que la petición no trae una sesión válida.
var errNoSession = errors.New("sin sesión válida")

// newToken genera un token aleatorio de 32 bytes (256 bits) para el cliente y el hash que
// se guarda en la base. Lo usan las sesiones y los tokens que se envían por correo.
func newToken() (token string, hash []byte, err error) {
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
// Las sesiones vencidas las borra el trabajo periódico de limpieza (internal/jobs).
func (s *Service) StartSession(ctx context.Context, w http.ResponseWriter, userID int64) error {
	token, hash, err := newToken()
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

	http.SetCookie(w, s.cookie(token, expires))
	return nil
}

// EndSession borra la sesión de la petición (si hay) y le indica al navegador que borre la cookie.
func (s *Service) EndSession(ctx context.Context, w http.ResponseWriter, r *http.Request) error {
	if c, err := r.Cookie(s.cookieName(CookieName)); err == nil {
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
	c, err := r.Cookie(s.cookieName(CookieName))
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

// setChallengeCookie envía la cookie del desafío del 2FA, con la misma protección que la de sesión.
func (s *Service) setChallengeCookie(w http.ResponseWriter, token string, expires time.Time) {
	c := s.cookie(token, expires)
	c.Name = s.cookieName(ChallengeCookieName)
	http.SetCookie(w, c)
}

// clearChallengeCookie le pide al navegador que borre la cookie del desafío.
func (s *Service) clearChallengeCookie(w http.ResponseWriter) {
	c := s.cookie("", time.Unix(0, 0))
	c.Name = s.cookieName(ChallengeCookieName)
	c.MaxAge = -1
	http.SetCookie(w, c)
}

// cookie arma la cookie de sesión:
//   - HttpOnly: JavaScript no puede leerla, así que un ataque XSS no puede robar la sesión.
//   - SameSite=Lax: el navegador no la envía en peticiones que otro sitio dispara
//     (por ejemplo un formulario oculto), la primera barrera contra CSRF.
//   - Secure: solo viaja por HTTPS (se desactiva en desarrollo, donde se usa http://localhost).
//   - Path=/ y sin Domain: vale para toda la API y solo para el host que la creó. Con Secure,
//     el nombre lleva el prefijo __Host-, que obliga al navegador a respetar estas tres reglas.
func (s *Service) cookie(value string, expires time.Time) *http.Cookie {
	return &http.Cookie{
		Name:     s.cookieName(CookieName),
		Value:    value,
		Path:     "/",
		Expires:  expires,
		HttpOnly: true,
		Secure:   s.opts.CookieSecure,
		SameSite: http.SameSiteLaxMode,
	}
}
