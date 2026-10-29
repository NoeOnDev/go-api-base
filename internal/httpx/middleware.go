package httpx

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"runtime/debug"
	"sync"
	"time"
)

// Middleware wraps an http.Handler to add behavior to it.
type Middleware func(http.Handler) http.Handler

// Chain applies the middlewares in the order given: the first one is the outermost, that is,
// the first to see the request and the last to see the response.
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

// RequestIDHeader is the header that carries the request ID.
const RequestIDHeader = "X-Request-ID"

// RequestID gives each request an ID. It keeps the one sent by a proxy (if it is reasonable)
// and otherwise generates one. It returns it in the response and stores in the context a logger
// that includes it in every line, to follow a request through the logs.
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

// RequestIDFrom returns the request ID, or "" if there is none.
func RequestIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey).(string)
	return id
}

// requestLogger is the logger of a request. It is stored as a pointer in the context so
// AddLogAttrs can extend it from an inner middleware and outer middlewares see the change too,
// like Logger when it writes the final line of the request.
type requestLogger struct {
	mu     sync.Mutex
	logger *slog.Logger
	quiet  bool // the final line is logged at debug except for errors (see Quiet)
}

// LoggerFrom returns the logger of the request (with its request_id and the attributes added
// with AddLogAttrs) or the default logger.
func LoggerFrom(ctx context.Context) *slog.Logger {
	if rl, ok := ctx.Value(loggerKey).(*requestLogger); ok {
		rl.mu.Lock()
		defer rl.mu.Unlock()
		return rl.logger
	}
	return slog.Default()
}

// AddLogAttrs adds attributes to every log line of the request, including the final Logger
// line. For example, auth adds the user_id when it identifies the user.
func AddLogAttrs(ctx context.Context, args ...any) {
	if rl, ok := ctx.Value(loggerKey).(*requestLogger); ok {
		rl.mu.Lock()
		defer rl.mu.Unlock()
		rl.logger = rl.logger.With(args...)
	}
}

func newRequestID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b) // crypto/rand.Read does not fail on the supported systems
	return hex.EncodeToString(b)
}

// validRequestID accepts only short IDs with safe characters, so a client cannot put arbitrary
// text in the logs.
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

// Logger logs one line per request with method, path, status, bytes and duration.
// 5xx responses are logged as errors, and those of routes wrapped in Quiet, at debug.
func Logger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}

		next.ServeHTTP(rec, r)

		level := slog.LevelInfo
		switch {
		case rec.status >= http.StatusInternalServerError:
			level = slog.LevelError
		case isQuiet(r.Context()):
			level = slog.LevelDebug
		}
		LoggerFrom(r.Context()).Log(r.Context(), level, "request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rec.status,
			"bytes", rec.bytes,
			"duration_ms", Milliseconds(time.Since(start)),
		)
	})
}

// Quiet makes the final Logger line of the route go at debug, unless the response is a server
// error (5xx), which is still logged as an error. It is for routes that are hit very often, like
// a monitor's health check, which would flood the log otherwise.
func Quiet(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if rl, ok := r.Context().Value(loggerKey).(*requestLogger); ok {
			rl.mu.Lock()
			rl.quiet = true
			rl.mu.Unlock()
		}
		next.ServeHTTP(w, r)
	})
}

// isQuiet reports whether the request went through Quiet.
func isQuiet(ctx context.Context) bool {
	rl, ok := ctx.Value(loggerKey).(*requestLogger)
	if !ok {
		return false
	}
	rl.mu.Lock()
	defer rl.mu.Unlock()
	return rl.quiet
}

// Milliseconds turns a duration into milliseconds with three decimals.
// slog would log a time.Duration as nanoseconds in JSON, which is hard to read.
func Milliseconds(d time.Duration) float64 {
	return float64(d.Microseconds()) / 1000
}

// statusRecorder keeps the status and the bytes written, to log them.
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

// Unwrap lets http.ResponseController reach the original ResponseWriter.
func (rec *statusRecorder) Unwrap() http.ResponseWriter { return rec.ResponseWriter }

// Recover catches the panics of the handlers, logs them with their stack trace and answers 500,
// so an unexpected error does not leave the connection without a response.
//
// If the response had already started (for example, in the middle of an SSE stream), a 500
// cannot be sent: the JSON would get mixed with what was already sent. In that case the error is
// logged and the connection is cut, with http.ErrAbortHandler.
func Recover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sw := &startedWriter{ResponseWriter: w}
		defer func() {
			v := recover()
			if v == nil {
				return
			}
			if v == http.ErrAbortHandler {
				// A deliberate net/http signal to abort the response: not an error.
				panic(v)
			}
			log := LoggerFrom(r.Context())
			if sw.started {
				log.Error("panic after the response started; cutting the connection", "panic", v, "stack", string(debug.Stack()))
				panic(http.ErrAbortHandler)
			}
			log.Error("panic recuperado", "panic", v, "stack", string(debug.Stack()))
			// The panic is already in the log, with its stack: answer without logging it again.
			writeBody(w, r, Internal(nil))
		}()
		next.ServeHTTP(sw, r)
	})
}

// startedWriter notes whether the response has started (the status or some byte was sent).
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

// Unwrap lets http.ResponseController reach the original writer (Flush, write deadlines).
func (w *startedWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// SecurityHeaders adds X-Content-Type-Options: nosniff to every response, which stops the
// browser from reading a response as another type of content (for example, a JSON as HTML or
// JavaScript).
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		next.ServeHTTP(w, r)
	})
}

// NoStore adds Cache-Control: no-store, so neither the browser nor a proxy keeps the response.
// It is used on the routes that return secrets or personal data (all of /auth).
func NoStore(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}
