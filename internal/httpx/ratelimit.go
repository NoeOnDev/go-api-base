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

// RateLimiter limita las peticiones por IP (o por bloque /64 en IPv6) con el algoritmo
// token bucket: cada IP tiene "burst" intentos disponibles, que se recargan de a uno cada "every".
//
// Guarda el estado en memoria: alcanza para una sola instancia de la API, que es el caso
// de un VPS. Con varias instancias detrás de un balanceador, cada una contaría por separado.
type RateLimiter struct {
	every      time.Duration
	burst      int
	trustProxy bool

	mu          sync.Mutex
	visitors    map[string]*visitor
	lastCleanup time.Time
}

type visitor struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

// visitorTTL es cuánto se recuerda una IP inactiva antes de olvidarla.
const visitorTTL = 10 * time.Minute

// NewRateLimiter crea un limitador que permite burst peticiones seguidas por IP
// y recarga una cada every. Por ejemplo, NewRateLimiter(12*time.Second, 5, false)
// permite 5 intentos seguidos y luego uno cada 12 segundos (5 por minuto).
func NewRateLimiter(every time.Duration, burst int, trustProxy bool) *RateLimiter {
	return &RateLimiter{
		every:       every,
		burst:       burst,
		trustProxy:  trustProxy,
		visitors:    map[string]*visitor{},
		lastCleanup: time.Now(),
	}
}

// Allow indica si la clave (una IP, o un bloque /64 para IPv6) puede hacer otra petición ahora.
func (rl *RateLimiter) Allow(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()

	// Limpieza de IPs inactivas, como mucho una vez por minuto, para que el mapa no crezca sin límite.
	if now.Sub(rl.lastCleanup) > time.Minute {
		for k, v := range rl.visitors {
			if now.Sub(v.lastSeen) > visitorTTL {
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

// rateLimitKey agrupa las direcciones IPv6 por su bloque /64. Un proveedor suele asignarle
// a cada cliente un /64 entero (miles de millones de direcciones), así que contar por dirección
// exacta permitiría esquivar el límite cambiando de IP. Las IPv4 se cuentan una por una.
func rateLimitKey(ip string) string {
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return ip
	}
	addr = addr.Unmap() // una IPv4 escrita como IPv6 (::ffff:1.2.3.4) se trata como IPv4
	if addr.Is4() {
		return addr.String()
	}
	prefix, err := addr.WithZone("").Prefix(64)
	if err != nil {
		return ip
	}
	return prefix.String()
}

// Middleware responde 429 con el encabezado Retry-After cuando la IP supera el límite.
func (rl *RateLimiter) Middleware(next http.Handler) http.Handler {
	retryAfter := strconv.Itoa(int(math.Ceil(rl.every.Seconds())))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := ClientIP(r, rl.trustProxy)
		if !rl.Allow(rateLimitKey(ip)) {
			LoggerFrom(r.Context()).Warn("límite de peticiones superado", "ip", ip, "path", r.URL.Path)
			w.Header().Set("Retry-After", retryAfter)
			WriteError(w, r, TooManyRequests())
			return
		}
		next.ServeHTTP(w, r)
	})
}
