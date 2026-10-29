// Package ratelimit limits the attempts per key (an email, a user) with the state stored in
// PostgreSQL. That way the limit is the same on every instance of the API and is not reset on a
// deploy. It is what stops brute force against an account: auth uses it in the login, the 2FA,
// the account actions and the emails.
//
// The per-IP limits, instead, are in memory (httpx.RateLimiter): they are a cheap first barrier
// that does not add a database write to each request. See docs/decisions.md.
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

// Limiter allows burst attempts in a row per key, and then refills one every every (like
// httpx.RateLimiter). It uses the GCRA algorithm: it stores a single date per key, and each
// attempt is a single atomic statement (TakeRateLimit), so two instances cannot step on each
// other.
//
// The keys that already refilled all their attempts are deleted by the hourly cleanup
// (internal/jobs).
type Limiter struct {
	q     *store.Queries
	name  string
	every time.Duration
	burst int
}

// New creates a limiter. name keeps its keys apart from those of other limiters: the same email
// counts separately in "login" and in another limit. For example, New(q, "login", time.Minute, 10)
// allows 10 attempts in a row and then one per minute.
func New(q *store.Queries, name string, every time.Duration, burst int) *Limiter {
	if name == "" || every <= 0 || burst < 1 {
		panic(fmt.Sprintf("ratelimit: invalid limit %q (every %v, burst %d)", name, every, burst))
	}
	return &Limiter{q: q, name: name, every: every, burst: burst}
}

// Allow spends one attempt of the key and reports whether it was allowed. If it was not, it
// spends nothing.
func (l *Limiter) Allow(ctx context.Context, key string) (bool, error) {
	n, err := l.q.TakeRateLimit(ctx, store.TakeRateLimitParams{Key: l.hash(key), Every: l.every, Burst: int32(l.burst)})
	if err != nil {
		return false, fmt.Errorf("limit %s: %w", l.name, err)
	}
	return n == 1, nil
}

// Reset gives the key back all its attempts. It is useful when what the limit protected changes:
// for example, after a password reset.
func (l *Limiter) Reset(ctx context.Context, key string) error {
	if err := l.q.DeleteRateLimit(ctx, l.hash(key)); err != nil {
		return fmt.Errorf("reset the limit %s: %w", l.name, err)
	}
	return nil
}

// RetryAfter is the value of the Retry-After header when the limit is exceeded: how long it
// takes to refill one attempt, in seconds.
func (l *Limiter) RetryAfter() string {
	return strconv.Itoa(int(math.Ceil(l.every.Seconds())))
}

// Middleware limits by the key that key returns, for example the user's id. When the limit is
// exceeded it responds 429 with the Retry-After header.
func (l *Limiter) Middleware(key func(*http.Request) string) httpx.Middleware {
	return func(next http.Handler) http.Handler {
		return httpx.Handle(func(w http.ResponseWriter, r *http.Request) error {
			ok, err := l.Allow(r.Context(), key(r))
			if err != nil {
				return err
			}
			if !ok {
				httpx.LoggerFrom(r.Context()).Warn("attempt limit exceeded", "limit", l.name, "path", r.URL.Path)
				w.Header().Set("Retry-After", l.RetryAfter())
				return httpx.TooManyRequests()
			}
			next.ServeHTTP(w, r)
			return nil
		})
	}
}

// hash is the key as it is stored in the database: hashed, because it can be an email.
func (l *Limiter) hash(key string) []byte {
	sum := sha256.Sum256([]byte(l.name + ":" + key))
	return sum[:]
}
