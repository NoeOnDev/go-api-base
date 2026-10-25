package httpx

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
	"sync"
	"time"
)

// Middleware envuelve un http.Handler para agregarle comportamiento.
type Middleware func(http.Handler) http.Handler

// Chain aplica los middlewares en el orden en que se reciben: el primero es el más externo,
// es decir, el primero en ver la petición y el último en ver la respuesta.
func Chain(h http.Handler, mws ...Middleware) http.Handler {
	for i := len(mws) - 1; i >= 0; i-- {
		h = mws[i](h)
	}
	return h
}

type ctxKey int

const (
	requestIDKey ctxKey = iota
	loggerKey
)

// RequestIDHeader es el encabezado que transporta el identificador de la petición.
const RequestIDHeader = "X-Request-ID"

// RequestID asigna un identificador a cada petición. Respeta el que envíe un proxy
// (si es razonable) y si no genera uno. Lo devuelve en la respuesta y guarda en el contexto
// un logger que lo incluye en cada línea, para poder seguir una petición en los logs.
func RequestID(logger *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := r.Header.Get(RequestIDHeader)
			if !validRequestID(id) {
				id = newRequestID()
			}
			w.Header().Set(RequestIDHeader, id)

			ctx := context.WithValue(r.Context(), requestIDKey, id)
			ctx = context.WithValue(ctx, loggerKey, &requestLogger{logger: logger.With("request_id", id)})
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// RequestIDFrom devuelve el identificador de la petición, o "" si no hay.
func RequestIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey).(string)
	return id
}

// requestLogger es el logger de una petición. Se guarda como puntero en el contexto para que
// AddLogAttrs pueda ampliarlo desde un middleware interno y el cambio lo vean también los
// middlewares externos, como Logger al escribir la línea final de la petición.
type requestLogger struct {
	mu     sync.Mutex
	logger *slog.Logger
}

// LoggerFrom devuelve el logger de la petición (con su request_id y los atributos agregados
// con AddLogAttrs) o el logger por defecto.
func LoggerFrom(ctx context.Context) *slog.Logger {
	if rl, ok := ctx.Value(loggerKey).(*requestLogger); ok {
		rl.mu.Lock()
		defer rl.mu.Unlock()
		return rl.logger
	}
	return slog.Default()
}

// AddLogAttrs agrega atributos a todas las líneas de log de la petición, incluida la línea
// final de Logger. Por ejemplo, auth agrega el user_id cuando identifica al usuario.
func AddLogAttrs(ctx context.Context, args ...any) {
	if rl, ok := ctx.Value(loggerKey).(*requestLogger); ok {
		rl.mu.Lock()
		defer rl.mu.Unlock()
		rl.logger = rl.logger.With(args...)
	}
}

func newRequestID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b) // crypto/rand.Read no falla en los sistemas soportados
	return hex.EncodeToString(b)
}

// validRequestID acepta solo identificadores cortos y con caracteres seguros,
// para que un cliente no pueda meter texto arbitrario en los logs.
func validRequestID(id string) bool {
	if id == "" || len(id) > 64 {
		return false
	}
	for _, c := range id {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

// Logger registra una línea por petición con método, ruta, status, bytes y duración.
// Las respuestas 5xx se registran como error.
func Logger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}

		next.ServeHTTP(rec, r)

		level := slog.LevelInfo
		if rec.status >= http.StatusInternalServerError {
			level = slog.LevelError
		}
		LoggerFrom(r.Context()).Log(r.Context(), level, "petición",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rec.status,
			"bytes", rec.bytes,
			"duration_ms", Milliseconds(time.Since(start)),
		)
	})
}

// Milliseconds convierte una duración a milisegundos con tres decimales.
// slog registraría un time.Duration en nanosegundos en JSON, que es difícil de leer.
func Milliseconds(d time.Duration) float64 {
	return float64(d.Microseconds()) / 1000
}

// statusRecorder guarda el status y los bytes escritos para poder registrarlos.
type statusRecorder struct {
	http.ResponseWriter
	status      int
	bytes       int
	wroteHeader bool
}

func (rec *statusRecorder) WriteHeader(status int) {
	if !rec.wroteHeader {
		rec.status = status
		rec.wroteHeader = true
	}
	rec.ResponseWriter.WriteHeader(status)
}

func (rec *statusRecorder) Write(b []byte) (int, error) {
	rec.wroteHeader = true
	n, err := rec.ResponseWriter.Write(b)
	rec.bytes += n
	return n, err
}

// Unwrap permite que http.ResponseController acceda al ResponseWriter original.
func (rec *statusRecorder) Unwrap() http.ResponseWriter { return rec.ResponseWriter }

// Recover captura los panics de los handlers, los registra con su stack trace y responde 500,
// para que un error inesperado no deje la conexión sin respuesta.
//
// Si la respuesta ya había empezado (por ejemplo, a mitad de un stream SSE), no se puede
// responder un 500: el JSON quedaría mezclado con lo que ya se envió. En ese caso se registra
// el error y se corta la conexión, con http.ErrAbortHandler.
func Recover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sw := &startedWriter{ResponseWriter: w}
		defer func() {
			v := recover()
			if v == nil {
				return
			}
			if v == http.ErrAbortHandler {
				// Señal intencional de net/http para abortar la respuesta: no es un error.
				panic(v)
			}
			log := LoggerFrom(r.Context())
			if sw.started {
				log.Error("panic con la respuesta ya empezada; se corta la conexión", "panic", v, "stack", string(debug.Stack()))
				panic(http.ErrAbortHandler)
			}
			log.Error("panic recuperado", "panic", v, "stack", string(debug.Stack()))
			Write(w, http.StatusInternalServerError, map[string]*Error{
				"error": Internal(fmt.Errorf("panic: %v", v)),
			})
		}()
		next.ServeHTTP(sw, r)
	})
}

// startedWriter anota si la respuesta ya empezó (se envió el status o algún byte).
type startedWriter struct {
	http.ResponseWriter
	started bool
}

func (w *startedWriter) WriteHeader(status int) {
	w.started = true
	w.ResponseWriter.WriteHeader(status)
}

func (w *startedWriter) Write(b []byte) (int, error) {
	w.started = true
	return w.ResponseWriter.Write(b)
}

// Unwrap deja que http.ResponseController llegue al writer original (Flush, plazos de escritura).
func (w *startedWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// SecurityHeaders agrega a todas las respuestas X-Content-Type-Options: nosniff, que le impide
// al navegador interpretar una respuesta como otro tipo de contenido (por ejemplo, un JSON
// como HTML o JavaScript).
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		next.ServeHTTP(w, r)
	})
}

// NoStore agrega Cache-Control: no-store, para que ni el navegador ni un proxy guarden la
// respuesta. Se usa en las rutas que devuelven secretos o datos personales (todo /auth).
func NoStore(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}
