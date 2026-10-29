// Package ratelimit limita los intentos por clave (un email, un usuario) con el estado guardado en
// PostgreSQL. Así el límite es el mismo en todas las instancias de la API y no se reinicia al
// desplegar. Es el que frena la fuerza bruta contra una cuenta: auth lo usa en el login, el 2FA,
// las acciones de la cuenta y los correos.
//
// Los límites por IP, en cambio, están en memoria (httpx.RateLimiter): son una primera barrera
// barata, que no le suma una escritura a la base por cada petición. Ver docs/decisions.md.
package ratelimit

import (
	"context"
	"crypto/sha256"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/NoeOnDev/go-api-base/internal/httpx"
	"github.com/NoeOnDev/go-api-base/internal/store"
)

// Limiter permite burst intentos seguidos por clave, y después recarga uno cada every (como
// httpx.RateLimiter). Usa el algoritmo GCRA: guarda una sola fecha por clave, y cada intento es
// una sola sentencia atómica (TakeRateLimit), así que dos instancias no pueden pisarse.
//
// Las claves que ya recargaron todos sus intentos las borra la limpieza de cada hora (internal/jobs).
type Limiter struct {
	q     *store.Queries
	name  string
	every time.Duration
	burst int
}

// New crea un limitador. name separa sus claves de las de otros limitadores: el mismo email
// cuenta por separado en "login" y en otro límite. Por ejemplo, New(q, "login", time.Minute, 10)
// permite 10 intentos seguidos y luego uno por minuto.
func New(q *store.Queries, name string, every time.Duration, burst int) *Limiter {
	if name == "" || every <= 0 || burst < 1 {
		panic(fmt.Sprintf("ratelimit: límite inválido %q (every %v, burst %d)", name, every, burst))
	}
	return &Limiter{q: q, name: name, every: every, burst: burst}
}

// Allow gasta un intento de la clave e indica si estaba permitido. Si no lo estaba, no gasta nada.
func (l *Limiter) Allow(ctx context.Context, key string) (bool, error) {
	n, err := l.q.TakeRateLimit(ctx, store.TakeRateLimitParams{Key: l.hash(key), Every: l.every, Burst: int32(l.burst)})
	if err != nil {
		return false, fmt.Errorf("límite %s: %w", l.name, err)
	}
	return n == 1, nil
}

// Reset le devuelve a la clave todos sus intentos. Sirve cuando cambia lo que el límite
// protegía: por ejemplo, después de recuperar la contraseña.
func (l *Limiter) Reset(ctx context.Context, key string) error {
	if err := l.q.DeleteRateLimit(ctx, l.hash(key)); err != nil {
		return fmt.Errorf("reiniciar el límite %s: %w", l.name, err)
	}
	return nil
}

// RetryAfter es el valor del encabezado Retry-After al superar el límite: lo que tarda en
// recargarse un intento, en segundos.
func (l *Limiter) RetryAfter() string {
	return strconv.Itoa(int(math.Ceil(l.every.Seconds())))
}

// Middleware limita por la clave que devuelve key, por ejemplo el id del usuario. Al superar el
// límite responde 429 con el encabezado Retry-After.
func (l *Limiter) Middleware(key func(*http.Request) string) httpx.Middleware {
	return func(next http.Handler) http.Handler {
		return httpx.Handle(func(w http.ResponseWriter, r *http.Request) error {
			ok, err := l.Allow(r.Context(), key(r))
			if err != nil {
				return err
			}
			if !ok {
				httpx.LoggerFrom(r.Context()).Warn("límite de intentos superado", "limit", l.name, "path", r.URL.Path)
				w.Header().Set("Retry-After", l.RetryAfter())
				return httpx.TooManyRequests()
			}
			next.ServeHTTP(w, r)
			return nil
		})
	}
}

// hash es la clave como se guarda en la base: hasheada, porque puede ser un email.
func (l *Limiter) hash(key string) []byte {
	sum := sha256.Sum256([]byte(l.name + ":" + key))
	return sum[:]
}
