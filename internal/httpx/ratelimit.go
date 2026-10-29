package httpx

import (
	"math"
	"net/http"
	"net/netip"
	"strconv"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// RateLimiter limits requests per IP (or per /64 block in IPv6) with the token bucket
// algorithm: each IP has "burst" attempts available, refilled one at a time every "every".
//
// It keeps its state in memory: with several API instances, each one counts on its own. That is
// why it is used for the per-IP limits, a first coarse and cheap barrier. The limits that stop
// brute force against an account are in PostgreSQL (internal/ratelimit) and hold for every instance.
type RateLimiter struct {
	every time.Duration
	burst int
	ttl   time.Duration // how long an idle key is remembered

	mu          sync.Mutex
	visitors    map[string]*visitor
	lastCleanup time.Time
}

type visitor struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

// visitorTTL is how long, at least, an idle key is remembered before it is forgotten.
// If the limiter takes longer to refill (every × burst), it is remembered until then:
// forgetting it earlier would give back all its attempts at once.
const visitorTTL = 10 * time.Minute

// NewRateLimiter creates a limiter that allows burst requests in a row per key and refills one
// every every. For example, NewRateLimiter(12*time.Second, 5) allows 5 attempts in a row and
// then one every 12 seconds (5 per minute). The middleware chooses the key: the IP
// (MiddlewareByIP) or another one, like the user (MiddlewareBy).
func NewRateLimiter(every time.Duration, burst int) *RateLimiter {
	return &RateLimiter{
		every:       every,
		burst:       burst,
		ttl:         max(visitorTTL, every*time.Duration(burst)),
		visitors:    map[string]*visitor{},
		lastCleanup: time.Now(),
	}
}

// Allow reports whether the key may make another request now. In the middleware the key is the
// IP (or its /64 block in IPv6); it also works for other keys, like a user or a recipient.
func (rl *RateLimiter) Allow(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()

	// Clean up idle keys, at most once a minute, so the map does not grow without bound.
	if now.Sub(rl.lastCleanup) > time.Minute {
		for k, v := range rl.visitors {
			if now.Sub(v.lastSeen) > rl.ttl {
				delete(rl.visitors, k)
			}
		}
		rl.lastCleanup = now
	}

	v, ok := rl.visitors[ip]
	if !ok {
		v = &visitor{limiter: rate.NewLimiter(rate.Every(rl.every), rl.burst)}
		rl.visitors[ip] = v
	}
	v.lastSeen = now

	return v.limiter.Allow()
}

// Reset forgets a key and gives back all its attempts. It is for when what the limit protected
// changes: for example, after a password reset.
func (rl *RateLimiter) Reset(key string) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	delete(rl.visitors, key)
}

// rateLimitKey groups IPv6 addresses by their /64 block. A provider usually gives each customer
// a whole /64 (billions of addresses), so counting by exact address would allow getting around
// the limit by changing IPs. IPv4 addresses are counted one by one.
func rateLimitKey(ip string) string {
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return ip
	}
	addr = addr.Unmap() // an IPv4 written as IPv6 (::ffff:1.2.3.4) is treated as IPv4
	if addr.Is4() {
		return addr.String()
	}
	prefix, err := addr.WithZone("").Prefix(64)
	if err != nil {
		return ip
	}
	return prefix.String()
}

// MiddlewareByIP limits by client IP (or by /64 block in IPv6). proxies are the proxies whose
// X-Forwarded-For is accepted (see ClientIP).
func (rl *RateLimiter) MiddlewareByIP(proxies TrustedProxies) Middleware {
	return rl.MiddlewareBy(func(r *http.Request) string {
		return rateLimitKey(ClientIP(r, proxies))
	})
}

// MiddlewareBy limits by the key that key returns, for example the user ID. Past the limit it
// answers 429 with the Retry-After header.
func (rl *RateLimiter) MiddlewareBy(key func(*http.Request) string) Middleware {
	retryAfter := strconv.Itoa(int(math.Ceil(rl.every.Seconds())))

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			k := key(r)
			if !rl.Allow(k) {
				LoggerFrom(r.Context()).Warn("rate limit exceeded", "key", k, "path", r.URL.Path)
				w.Header().Set("Retry-After", retryAfter)
				WriteError(w, r, TooManyRequests())
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
