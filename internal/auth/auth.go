// Package auth implementa la autenticación: registro, login con sesiones guardadas en
// PostgreSQL y enviadas en una cookie HttpOnly, roles (user/admin) y los middlewares
// que protegen las rutas.
package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/NoeOnDev/go-api-base/internal/db"
	"github.com/NoeOnDev/go-api-base/internal/httpx"
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
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	Email     string    `json:"email"`
	Role      string    `json:"role"`
	CreatedAt time.Time `json:"created_at"`
}

// IsAdmin indica si el usuario tiene rol de administrador.
func (u User) IsAdmin() bool { return u.Role == RoleAdmin }

func toUser(u store.User) User {
	return User{ID: u.ID, Name: u.Name, Email: u.Email, Role: u.Role, CreatedAt: u.CreatedAt}
}

// Options configura el servicio de autenticación.
type Options struct {
	// SessionTTL es cuánto dura una sesión. Si es 0 se usa DefaultSessionTTL.
	SessionTTL time.Duration
	// CookieSecure hace que la cookie solo viaje por HTTPS. Debe ser true en producción.
	CookieSecure bool
}

// Service agrupa la lógica de autenticación y sus dependencias.
type Service struct {
	q    *store.Queries
	opts Options
}

// NewService crea el servicio de autenticación.
func NewService(q *store.Queries, opts Options) *Service {
	if opts.SessionTTL <= 0 {
		opts.SessionTTL = DefaultSessionTTL
	}
	return &Service{q: q, opts: opts}
}

// CreateUser valida los datos, hashea la contraseña y crea el usuario con el rol indicado.
// Lo usan el registro (rol user) y el comando create-admin (rol admin).
// Los errores de validación y el email repetido se devuelven como *httpx.Error.
func (s *Service) CreateUser(ctx context.Context, in RegisterInput, role string) (User, error) {
	in.normalize()
	if err := in.validate(); err != nil {
		return User{}, err
	}
	if role != RoleUser && role != RoleAdmin {
		return User{}, fmt.Errorf("rol desconocido %q", role)
	}

	hash, err := HashPassword(in.Password)
	if err != nil {
		return User{}, fmt.Errorf("hashear contraseña: %w", err)
	}

	u, err := s.q.CreateUser(ctx, store.CreateUserParams{
		Name:         in.Name,
		Email:        in.Email,
		PasswordHash: hash,
		Role:         role,
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
		_, _ = VerifyPassword(password, dummyHash())
		return User{}, errInvalidCredentials
	}
	if err != nil {
		return User{}, fmt.Errorf("buscar usuario: %w", err)
	}

	ok, err := VerifyPassword(password, u.PasswordHash)
	if err != nil {
		return User{}, fmt.Errorf("verificar contraseña del usuario %d: %w", u.ID, err)
	}
	if !ok {
		return User{}, errInvalidCredentials
	}

	return toUser(u), nil
}
