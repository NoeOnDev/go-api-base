package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"github.com/NoeOnDev/go-api-base/internal/httpx"
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

// maxUserAgentLen es cuánto del user-agent se guarda con la sesión.
const maxUserAgentLen = 512

// touchInterval es cada cuánto se actualiza la última actividad de una sesión. Actualizarla en
// cada petición sería una escritura en la base por petición; con 5 minutos alcanza para mostrar
// "activa hace un rato" y para el vencimiento por inactividad.
const touchInterval = 5 * time.Minute

// StartSession crea una sesión para el usuario y envía la cookie. Guarda el navegador
// (user-agent) y la IP de la petición, para que el usuario reconozca sus dispositivos en
// GET /auth/sessions. Las sesiones vencidas las borra la limpieza periódica (internal/jobs).
func (s *Service) StartSession(w http.ResponseWriter, r *http.Request, userID int64) error {
	token, hash, err := newToken()
	if err != nil {
		return err
	}

	expires := time.Now().Add(s.opts.SessionTTL)
	if err := s.q.CreateSession(r.Context(), store.CreateSessionParams{
		TokenHash: hash,
		UserID:    userID,
		ExpiresAt: expires,
		UserAgent: truncate(strings.ToValidUTF8(r.UserAgent(), ""), maxUserAgentLen),
		IP:        httpx.ClientIP(r, s.opts.TrustedProxies),
	}); err != nil {
		return fmt.Errorf("crear sesión: %w", err)
	}

	http.SetCookie(w, s.cookie(token, expires))
	return nil
}

// EndSession borra la sesión de la petición (si hay) y le indica al navegador que borre la cookie.
func (s *Service) EndSession(ctx context.Context, w http.ResponseWriter, r *http.Request) error {
	if c, err := r.Cookie(s.cookieName(CookieName)); err == nil {
		hash := hashToken(c.Value)
		if err := s.q.DeleteSession(ctx, hash); err != nil {
			return fmt.Errorf("borrar sesión: %w", err)
		}
		if s.opts.SessionWatcher != nil {
			s.opts.SessionWatcher.SessionEnded(ctx, hex.EncodeToString(hash))
		}
	}

	expired := s.cookie("", time.Unix(0, 0))
	expired.MaxAge = -1
	http.SetCookie(w, expired)
	return nil
}

// SessionKey identifica la sesión de la petición, o devuelve "" si no hay cookie. Es el hash
// del token (el mismo que se guarda en la base), así que no sirve para iniciar sesión.
// La usan los streams de GET /events para cortarse cuando se cierra su sesión.
func (s *Service) SessionKey(r *http.Request) string {
	c, err := r.Cookie(s.cookieName(CookieName))
	if err != nil {
		return ""
	}
	return hex.EncodeToString(hashToken(c.Value))
}

// CheckSession comprueba que la sesión de la petición siga vigente: que no se haya cerrado ni
// vencido, y que el usuario exista. Sirve para las conexiones largas, que pasaron por
// RequireUser al abrirse pero siguen abiertas después (ver internal/realtime).
func (s *Service) CheckSession(r *http.Request) error {
	_, err := s.userFromRequest(r)
	return err
}

// session es la sesión de una petición.
type session struct {
	id        int64
	tokenHash []byte
	user      User
	lastSeen  time.Time
}

// sessionFromRequest busca la sesión enviada en la cookie, con su usuario.
// Devuelve errNoSession si no hay cookie, la sesión no existe o venció.
func (s *Service) sessionFromRequest(r *http.Request) (session, error) {
	c, err := r.Cookie(s.cookieName(CookieName))
	if err != nil {
		return session{}, errNoSession
	}

	hash := hashToken(c.Value)
	row, err := s.q.GetSessionUser(r.Context(), hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return session{}, errNoSession
	}
	if err != nil {
		return session{}, fmt.Errorf("buscar sesión: %w", err)
	}
	return session{id: row.SessionID, tokenHash: hash, user: toUser(row.User), lastSeen: row.LastSeenAt}, nil
}

// userFromRequest busca el usuario de la sesión enviada en la cookie.
func (s *Service) userFromRequest(r *http.Request) (User, error) {
	sess, err := s.sessionFromRequest(r)
	return sess.user, err
}

// touch actualiza la última actividad de la sesión, como mucho cada touchInterval. Si falla,
// solo se registra: no es motivo para rechazar la petición.
func (s *Service) touch(r *http.Request, sess session) {
	if time.Since(sess.lastSeen) < touchInterval {
		return
	}
	if err := s.q.TouchSession(r.Context(), sess.tokenHash); err != nil {
		httpx.LoggerFrom(r.Context()).Warn("no se pudo actualizar la actividad de la sesión", "error", err)
	}
}

// truncate corta s a como mucho n bytes, sin partir un carácter.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
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
