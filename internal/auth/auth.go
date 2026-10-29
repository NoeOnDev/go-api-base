// Package auth implementa la autenticación: registro, login con sesiones guardadas en
// PostgreSQL y enviadas en una cookie HttpOnly, roles (user/admin) y los middlewares
// que protegen las rutas.
package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/NoeOnDev/go-api-base/internal/db"
	"github.com/NoeOnDev/go-api-base/internal/httpx"
	"github.com/NoeOnDev/go-api-base/internal/mail"
	"github.com/NoeOnDev/go-api-base/internal/ratelimit"
	"github.com/NoeOnDev/go-api-base/internal/secretbox"
	"github.com/NoeOnDev/go-api-base/internal/store"
)

// Roles de usuario.
const (
	RoleUser  = "user"
	RoleAdmin = "admin"
)

// DefaultSessionTTL se usa si no se configura la duración de la sesión.
const DefaultSessionTTL = 7 * 24 * time.Hour

// User es el usuario autenticado. Es lo que reciben los handlers y lo que devuelve la API:
// nunca incluye el hash de la contraseña.
type User struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
	Role  string `json:"role"`
	// EmailVerifiedAt es null mientras el usuario no confirme su email.
	EmailVerifiedAt  *time.Time `json:"email_verified_at"`
	TwoFactorEnabled bool       `json:"two_factor_enabled"`
	CreatedAt        time.Time  `json:"created_at"`
}

// EmailVerified indica si el usuario confirmó su email.
func (u User) EmailVerified() bool { return u.EmailVerifiedAt != nil }

// IsAdmin indica si el usuario tiene rol de administrador.
func (u User) IsAdmin() bool { return u.Role == RoleAdmin }

func toUser(u store.User) User {
	return User{
		ID:               u.ID,
		Name:             u.Name,
		Email:            u.Email,
		Role:             u.Role,
		EmailVerifiedAt:  u.EmailVerifiedAt,
		TwoFactorEnabled: u.TOTPEnabledAt != nil,
		CreatedAt:        u.CreatedAt,
	}
}

// Options configura el servicio de autenticación.
type Options struct {
	// SessionTTL es cuánto dura una sesión. Si es 0 se usa DefaultSessionTTL.
	SessionTTL time.Duration
	// SessionIdleTimeout cierra una sesión sin actividad durante ese tiempo. 0 lo desactiva.
	SessionIdleTimeout time.Duration
	// CookieSecure hace que la cookie solo viaje por HTTPS. Debe ser true en producción.
	CookieSecure bool
	// Mailer envía los correos (recuperar contraseña, verificar email). Si es nil,
	// los correos se escriben en el log.
	Mailer mail.Sender
	// FrontendURL es la base de los enlaces de los correos, por ejemplo https://app.cliente.com.
	FrontendURL string
	// RequireVerifiedEmail exige el email verificado en RequireUser y RequireAdmin.
	RequireVerifiedEmail bool
	// Box cifra el secreto del 2FA y protege los códigos de recuperación. Sin él, el 2FA no está disponible.
	Box *secretbox.Box
	// Issuer es el nombre de la aplicación que muestra la app de autenticación.
	Issuer string
	// TrustedProxies son los proxies de los que se acepta X-Forwarded-For (ver httpx.ClientIP).
	// Se usan para registrar la IP real del cliente (por ejemplo, en el correo que avisa de
	// códigos del 2FA incorrectos).
	TrustedProxies httpx.TrustedProxies
	// SessionWatcher se entera cuando auth cierra sesiones, para cortar lo que dependía de ellas.
	// Es opcional. En la plantilla lo implementa el broker de internal/realtime, que corta los
	// streams de GET /events abiertos con esas sesiones.
	SessionWatcher SessionWatcher
	// Notifier recibe los avisos de seguridad de la cuenta (contraseña cambiada, 2FA activado
	// o desactivado). Es opcional: si es nil, auth no notifica. En la plantilla lo implementa
	// internal/notifications, así auth no depende de ese módulo.
	Notifier Notifier
}

// SessionWatcher recibe los cierres de sesión. sessionKey es la de Service.SessionKey.
type SessionWatcher interface {
	// SessionEnded avisa que se cerró una sesión (logout).
	SessionEnded(ctx context.Context, sessionKey string)
	// UserSessionsEnded avisa que se cerraron todas las sesiones del usuario salvo
	// keepSessionKey (vacío: todas).
	UserSessionsEnded(ctx context.Context, userID int64, keepSessionKey string)
}

// Notifier envía una notificación a un usuario (ver internal/notifications).
type Notifier interface {
	Notify(ctx context.Context, userID int64, kind string, data any) error
}

// Tipos de las notificaciones de seguridad que envía auth.
const (
	NotifyPasswordChanged   = "password_changed"
	NotifyTwoFactorEnabled  = "two_factor_enabled"
	NotifyTwoFactorDisabled = "two_factor_disabled"
	NotifyEmailChanged      = "email_changed"
)

// notify avisa al usuario de un cambio de seguridad en su cuenta. Si falla, solo se registra:
// el cambio ya se hizo, y no tiene sentido responder un error por el aviso.
func (s *Service) notify(ctx context.Context, userID int64, kind, message string) {
	if s.opts.Notifier == nil {
		return
	}
	if err := s.opts.Notifier.Notify(ctx, userID, kind, map[string]string{"message": message}); err != nil {
		httpx.LoggerFrom(ctx).Warn("no se pudo enviar la notificación", "user_id", userID, "kind", kind, "error", err)
	}
}

// resetLimit le devuelve a la clave todos sus intentos. Si falla, solo se registra: lo que el
// límite protegía ya cambió, y la clave se recarga sola con el tiempo.
func (s *Service) resetLimit(ctx context.Context, l *ratelimit.Limiter, key string) {
	if err := l.Reset(ctx, key); err != nil {
		httpx.LoggerFrom(ctx).Warn("no se pudo reiniciar un límite de intentos", "error", err)
	}
}

// Service agrupa la lógica de autenticación y sus dependencias.
type Service struct {
	db   *pgxpool.Pool // para las transacciones (pgx.BeginFunc)
	q    *store.Queries
	opts Options

	// Límites que no dependen de la IP (ver handlers.go, two_factor_login.go y emails.go). Están en
	// la base, así valen para todas las instancias de la API.
	loginLimiter     *ratelimit.Limiter
	twoFactorLimiter *ratelimit.Limiter
	emailLimiter     *ratelimit.Limiter
}

// NewService crea el servicio de autenticación. Recibe el pool, y no solo las consultas,
// porque algunas operaciones van en una transacción (por ejemplo, recuperar la contraseña).
func NewService(pool *pgxpool.Pool, opts Options) *Service {
	if opts.SessionTTL <= 0 {
		opts.SessionTTL = DefaultSessionTTL
	}
	if opts.Mailer == nil {
		opts.Mailer = mail.LogSender{Logger: slog.Default()}
	}
	if opts.FrontendURL == "" {
		opts.FrontendURL = "http://localhost:5173"
	}
	if opts.Issuer == "" {
		opts.Issuer = "Mi App"
	}
	q := store.New(pool)
	return &Service{
		db:               pool,
		q:                q,
		opts:             opts,
		loginLimiter:     ratelimit.New(q, "login", loginLimitEvery, loginLimitBurst),
		twoFactorLimiter: ratelimit.New(q, "two_factor", twoFactorLimitEvery, twoFactorLimitBurst),
		emailLimiter:     ratelimit.New(q, "email", emailLimitEvery, emailLimitBurst),
	}
}

// CreateUser valida los datos, hashea la contraseña y crea el usuario con el rol indicado.
// Lo usan el registro (rol user) y el comando create-admin (rol admin). Los admins quedan
// con el email verificado: los crea alguien con acceso al servidor.
// Los errores de validación y el email repetido se devuelven como *httpx.Error.
func (s *Service) CreateUser(ctx context.Context, in RegisterInput, role string) (User, error) {
	in.normalize()
	if err := in.validate(); err != nil {
		return User{}, err
	}
	if role != RoleUser && role != RoleAdmin {
		return User{}, fmt.Errorf("rol desconocido %q", role)
	}

	hash, err := HashPassword(ctx, in.Password)
	if err != nil {
		return User{}, fmt.Errorf("hashear contraseña: %w", err)
	}

	var verifiedAt *time.Time
	if role == RoleAdmin {
		now := time.Now()
		verifiedAt = &now
	}

	u, err := s.q.CreateUser(ctx, store.CreateUserParams{
		Name:            in.Name,
		Email:           in.Email,
		PasswordHash:    hash,
		Role:            role,
		EmailVerifiedAt: verifiedAt,
	})
	if _, ok := db.IsUniqueViolation(err); ok {
		return User{}, httpx.Conflict("el email ya está registrado", "email")
	}
	if err != nil {
		return User{}, fmt.Errorf("crear usuario: %w", err)
	}

	return toUser(u), nil
}

// errInvalidCredentials es la respuesta a cualquier login fallido. No distingue entre
// "el email no existe" y "la contraseña es incorrecta", para no revelar qué emails están registrados.
var errInvalidCredentials = httpx.Unauthorized("email o contraseña incorrectos")

// Authenticate comprueba email y contraseña y devuelve el usuario.
func (s *Service) Authenticate(ctx context.Context, email, password string) (User, error) {
	u, err := s.q.GetUserByEmail(ctx, strings.ToLower(strings.TrimSpace(email)))
	if errors.Is(err, pgx.ErrNoRows) {
		// Se calcula un hash igual, para que la respuesta tarde lo mismo que con un email válido.
		_, _ = VerifyPassword(ctx, password, dummyHash())
		return User{}, errInvalidCredentials
	}
	if err != nil {
		return User{}, fmt.Errorf("buscar usuario: %w", err)
	}

	ok, err := VerifyPassword(ctx, password, u.PasswordHash)
	if err != nil {
		return User{}, fmt.Errorf("verificar contraseña del usuario %d: %w", u.ID, err)
	}
	if !ok {
		return User{}, errInvalidCredentials
	}
	if needsRehash(u.PasswordHash) {
		s.rehashPassword(ctx, u, password)
	}

	return toUser(u), nil
}

// rehashPassword vuelve a hashear la contraseña con los parámetros actuales de argon2, después
// de un login correcto (es el único momento en que se tiene la contraseña). Si falla, solo se
// registra: el login ya fue válido, y se vuelve a intentar en el próximo.
func (s *Service) rehashPassword(ctx context.Context, u store.User, password string) {
	hash, err := HashPassword(ctx, password)
	if err == nil {
		_, err = s.q.RehashPassword(ctx, store.RehashPasswordParams{ID: u.ID, OldHash: u.PasswordHash, NewHash: hash})
	}
	if err != nil {
		httpx.LoggerFrom(ctx).Warn("no se pudo actualizar el hash de la contraseña", "user_id", u.ID, "error", err)
	}
}
