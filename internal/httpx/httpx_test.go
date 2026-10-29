package httpx

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/NoeOnDev/go-api-base/internal/i18n"
)

type payload struct {
	Name string `json:"name"`
	Age  int    `json:"age"`
}

func TestDecode(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		wantStatus int // 0 means no error is expected
	}{
		{"valid", `{"name":"Alice","age":30}`, 0},
		{"empty", ``, http.StatusBadRequest},
		{"malformed", `{"name":`, http.StatusBadRequest},
		{"invalid syntax", `{"name" "Alice"}`, http.StatusBadRequest},
		{"wrong type", `{"age":"thirty"}`, http.StatusBadRequest},
		{"unknown field", `{"name":"Alice","admin":true}`, http.StatusBadRequest},
		{"two objects", `{"name":"Alice"}{"name":"Bob"}`, http.StatusBadRequest},
		{"too large", `{"name":"` + strings.Repeat("a", MaxBodyBytes) + `"}`, http.StatusRequestEntityTooLarge},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(tt.body))
			w := httptest.NewRecorder()

			var dst payload
			err := Decode(w, r, &dst)

			if tt.wantStatus == 0 {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}

			var apiErr *Error
			if !errors.As(err, &apiErr) {
				t.Fatalf("want *Error, got %v", err)
			}
			if apiErr.Status != tt.wantStatus {
				t.Errorf("status = %d, want %d", apiErr.Status, tt.wantStatus)
			}
		})
	}
}

func decodeErrorBody(t *testing.T, body *bytes.Buffer) errorBody {
	t.Helper()
	var resp struct {
		Error errorBody `json:"error"`
	}
	if err := json.NewDecoder(body).Decode(&resp); err != nil {
		t.Fatalf("the response is not valid JSON: %v", err)
	}
	return resp.Error
}

func TestWriteErrorFormats(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)

	t.Run("API error", func(t *testing.T) {
		w := httptest.NewRecorder()
		WriteError(w, r, Validation(Fields{"title": i18n.M("validation.required")}))

		if w.Code != http.StatusUnprocessableEntity {
			t.Errorf("status = %d", w.Code)
		}
		got := decodeErrorBody(t, w.Body)
		if got.Code != "validation_failed" || got.Message != "The data sent is not valid." || got.Fields["title"] != "This field is required." {
			t.Errorf("unexpected body: %+v", got)
		}
	})

	t.Run("in the language of the request", func(t *testing.T) {
		w := httptest.NewRecorder()
		es := r.WithContext(i18n.WithTranslator(r.Context(), i18n.Default().Translator("es-MX")))
		WriteError(w, es, Validation(Fields{"title": i18n.M("validation.too_long", "max", 200)}))

		got := decodeErrorBody(t, w.Body)
		if got.Message != "Los datos enviados no son válidos." || got.Fields["title"] != "Debe tener como máximo 200 caracteres." {
			t.Errorf("unexpected body: %+v", got)
		}
	})

	t.Run("internal error does not expose details", func(t *testing.T) {
		w := httptest.NewRecorder()
		WriteError(w, r, errors.New("database password: secret"))

		if w.Code != http.StatusInternalServerError {
			t.Errorf("status = %d", w.Code)
		}
		if strings.Contains(w.Body.String(), "database password") {
			t.Error("the response exposes the internal error")
		}
		if got := decodeErrorBody(t, w.Body); got.Code != "internal_error" {
			t.Errorf("code = %q", got.Code)
		}
	})
}

func TestRequestID(t *testing.T) {
	var seen string
	h := Chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = RequestIDFrom(r.Context())
	}), RequestID(slog.Default()))

	t.Run("generates a new one", func(t *testing.T) {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
		if len(seen) != 32 || w.Header().Get(RequestIDHeader) != seen {
			t.Errorf("id = %q, header = %q", seen, w.Header().Get(RequestIDHeader))
		}
	})

	t.Run("keeps a valid one", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.Header.Set(RequestIDHeader, "abc-123")
		h.ServeHTTP(httptest.NewRecorder(), r)
		if seen != "abc-123" {
			t.Errorf("id = %q", seen)
		}
	})

	t.Run("replaces an invalid one", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.Header.Set(RequestIDHeader, "injection\nin logs")
		h.ServeHTTP(httptest.NewRecorder(), r)
		if seen == "injection\nin logs" || len(seen) != 32 {
			t.Errorf("id = %q", seen)
		}
	})
}

func TestRecover(t *testing.T) {
	h := Chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("something went wrong")
	}), RequestID(slog.Default()), Logger, Recover)

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d", w.Code)
	}
	if got := decodeErrorBody(t, w.Body); got.Code != "internal_error" {
		t.Errorf("code = %q", got.Code)
	}
}

func TestHandle(t *testing.T) {
	h := Handle(func(w http.ResponseWriter, r *http.Request) error {
		return NotFound("note_not_found")
	})

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d", w.Code)
	}
	if got := decodeErrorBody(t, w.Body); got.Code != "note_not_found" || got.Message != "The note does not exist." {
		t.Errorf("error = %+v", got)
	}
}

func TestAddLogAttrsReachesRequestLog(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))

	// An inner middleware (like auth) adds the user_id; the final Logger line must include it.
	h := Chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		AddLogAttrs(r.Context(), "user_id", 42)
		w.WriteHeader(http.StatusOK)
	}), RequestID(logger), Logger)

	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))

	if line := buf.String(); !strings.Contains(line, "msg=request") || !strings.Contains(line, "user_id=42") {
		t.Errorf("the request line does not include the user_id: %s", line)
	}
}

// A response that cannot be encoded is a programming error: it used to send a 200 with a cut
// body, and the error was logged without the request_id. Now it is encoded before sending
// anything, so Recover answers a proper 500 and logs it with the request_id.
func TestWriteUnencodableResponse(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	h := Chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		Write(w, http.StatusOK, map[string]any{"canal": make(chan int)})
	}), RequestID(logger), Logger, Recover)

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", w.Code)
	}
	if got := decodeErrorBody(t, w.Body); got.Code != "internal_error" {
		t.Errorf("code = %q", got.Code)
	}
	logs := buf.String()
	if !strings.Contains(logs, "encode the response") || !strings.Contains(logs, "request_id="+w.Header().Get("X-Request-ID")) {
		t.Errorf("the error was not logged with the request_id:\n%s", logs)
	}
}

// A panic once the response has started (for example, in the middle of an SSE stream) cannot
// append an error JSON to what was already sent: the connection is cut.
func TestRecoverAfterResponseStarted(t *testing.T) {
	var buf bytes.Buffer
	var mu sync.Mutex
	logger := slog.New(slog.NewTextHandler(&lockedWriter{w: &buf, mu: &mu}, nil))
	srv := httptest.NewServer(Chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("data: part\n\n"))
		_ = http.NewResponseController(w).Flush()
		panic("fails in the middle of the stream")
	}), RequestID(logger), Logger, Recover))
	t.Cleanup(srv.Close)

	resp, err := srv.Client().Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	body, readErr := io.ReadAll(resp.Body)
	resp.Body.Close()

	if strings.Contains(string(body), "internal_error") {
		t.Errorf("the error JSON got mixed with the stream: %q", body)
	}
	if readErr == nil {
		t.Error("the connection should be cut instead of ending the response normally")
	}
	mu.Lock()
	logs := buf.String()
	mu.Unlock()
	if !strings.Contains(logs, "fails in the middle of the stream") || !strings.Contains(logs, "request_id="+resp.Header.Get("X-Request-ID")) {
		t.Errorf("the panic was not logged with the request_id:\n%s", logs)
	}
}

// lockedWriter guards the log buffer, which the handler and the server both write.
type lockedWriter struct {
	w  io.Writer
	mu *sync.Mutex
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

func TestParsePage(t *testing.T) {
	tests := []struct {
		query     string
		want      Page
		badFields []string
	}{
		{"", Page{Limit: DefaultLimit}, nil},
		{"limit=1&offset=0", Page{Limit: 1}, nil},
		{"limit=100&offset=10000", Page{Limit: MaxLimit, Offset: MaxOffset}, nil},
		{"limit=0", Page{}, []string{"limit"}},
		{"limit=101", Page{}, []string{"limit"}},
		// A huge offset makes the query slow: the cap is MaxOffset.
		{"offset=10001", Page{}, []string{"offset"}},
		{"offset=-1", Page{}, []string{"offset"}},
		{"limit=ten&offset=abc", Page{}, []string{"limit", "offset"}},
	}
	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			page, err := ParsePage(httptest.NewRequest(http.MethodGet, "/notes?"+tt.query, nil))
			if tt.badFields == nil {
				if err != nil || page != tt.want {
					t.Errorf("ParsePage = %+v, %v; want %+v", page, err, tt.want)
				}
				return
			}
			var apiErr *Error
			if !errors.As(err, &apiErr) || apiErr.Status != http.StatusUnprocessableEntity {
				t.Fatalf("want a validation error, got %v", err)
			}
			for _, f := range tt.badFields {
				if apiErr.Fields[f].IsZero() {
					t.Errorf("missing the error of the field %q: %v", f, apiErr.Fields)
				}
			}
		})
	}
}

// With Quiet, the request line is logged at debug, except for a server error.
func TestQuietLogsAtDebug(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	respond := func(status int) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(status) })
	}
	logLine := func(h http.Handler) string {
		buf.Reset()
		Chain(h, RequestID(logger), Logger).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/healthz", nil))
		return buf.String()
	}

	for name, tt := range map[string]struct {
		handler http.Handler
		level   string
	}{
		"with Quiet and 200": {Quiet(respond(http.StatusOK)), "level=DEBUG"},
		"with Quiet and 503": {Quiet(respond(http.StatusServiceUnavailable)), "level=ERROR"},
		"without Quiet, 200": {respond(http.StatusOK), "level=INFO"},
	} {
		if got := logLine(tt.handler); !strings.Contains(got, tt.level) {
			t.Errorf("%s: want %s in %q", name, tt.level, got)
		}
	}
}
