package httpx

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
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
			ctx = context.WithValue(ctx, loggerKey, logger.With("request_id", id))
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// RequestIDFrom devuelve el identificador de la petición, o "" si no hay.
func RequestIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey).(string)
	return id
}

// LoggerFrom devuelve el logger de la petición (con su request_id) o el logger por defecto.
func LoggerFrom(ctx context.Context) *slog.Logger {
	if l, ok := ctx.Value(loggerKey).(*slog.Logger); ok {
		return l
	}
	return slog.Default()
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
			"duration", time.Since(start),
		)
	})
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
func Recover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			v := recover()
			if v == nil {
				return
			}
			if v == http.ErrAbortHandler {
				// Señal intencional de net/http para abortar la respuesta: no es un error.
				panic(v)
			}
			LoggerFrom(r.Context()).Error("panic recuperado", "panic", v, "stack", string(debug.Stack()))
			Write(w, http.StatusInternalServerError, map[string]*Error{
				"error": Internal(fmt.Errorf("panic: %v", v)),
			})
		}()
		next.ServeHTTP(w, r)
	})
}
