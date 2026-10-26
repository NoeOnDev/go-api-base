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
)

type payload struct {
	Name string `json:"name"`
	Age  int    `json:"age"`
}

func TestDecode(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		wantStatus int // 0 significa que no se espera error
	}{
		{"válido", `{"name":"Ana","age":30}`, 0},
		{"vacío", ``, http.StatusBadRequest},
		{"mal formado", `{"name":`, http.StatusBadRequest},
		{"sintaxis inválida", `{"name" "Ana"}`, http.StatusBadRequest},
		{"tipo incorrecto", `{"age":"treinta"}`, http.StatusBadRequest},
		{"campo desconocido", `{"name":"Ana","admin":true}`, http.StatusBadRequest},
		{"dos objetos", `{"name":"Ana"}{"name":"Luis"}`, http.StatusBadRequest},
		{"demasiado grande", `{"name":"` + strings.Repeat("a", MaxBodyBytes) + `"}`, http.StatusRequestEntityTooLarge},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(tt.body))
			w := httptest.NewRecorder()

			var dst payload
			err := Decode(w, r, &dst)

			if tt.wantStatus == 0 {
				if err != nil {
					t.Fatalf("error inesperado: %v", err)
				}
				return
			}

			var apiErr *Error
			if !errors.As(err, &apiErr) {
				t.Fatalf("se esperaba *Error, se obtuvo %v", err)
			}
			if apiErr.Status != tt.wantStatus {
				t.Errorf("status = %d, se esperaba %d", apiErr.Status, tt.wantStatus)
			}
		})
	}
}

func decodeErrorBody(t *testing.T, body *bytes.Buffer) Error {
	t.Helper()
	var resp struct {
		Error Error `json:"error"`
	}
	if err := json.NewDecoder(body).Decode(&resp); err != nil {
		t.Fatalf("respuesta no es JSON válido: %v", err)
	}
	return resp.Error
}

func TestWriteErrorFormats(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)

	t.Run("error de la API", func(t *testing.T) {
		w := httptest.NewRecorder()
		WriteError(w, r, Validation(map[string]string{"title": "es obligatorio"}))

		if w.Code != http.StatusUnprocessableEntity {
			t.Errorf("status = %d", w.Code)
		}
		got := decodeErrorBody(t, w.Body)
		if got.Code != "validation_failed" || got.Fields["title"] != "es obligatorio" {
			t.Errorf("cuerpo inesperado: %+v", got)
		}
	})

	t.Run("error interno no expone detalles", func(t *testing.T) {
		w := httptest.NewRecorder()
		WriteError(w, r, errors.New("contraseña de la base: secreta"))

		if w.Code != http.StatusInternalServerError {
			t.Errorf("status = %d", w.Code)
		}
		if strings.Contains(w.Body.String(), "secreta") {
			t.Error("la respuesta expone el error interno")
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

	t.Run("genera uno nuevo", func(t *testing.T) {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
		if len(seen) != 32 || w.Header().Get(RequestIDHeader) != seen {
			t.Errorf("id = %q, encabezado = %q", seen, w.Header().Get(RequestIDHeader))
		}
	})

	t.Run("respeta uno válido", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.Header.Set(RequestIDHeader, "abc-123")
		h.ServeHTTP(httptest.NewRecorder(), r)
		if seen != "abc-123" {
			t.Errorf("id = %q", seen)
		}
	})

	t.Run("reemplaza uno inválido", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.Header.Set(RequestIDHeader, "inyección\nen logs")
		h.ServeHTTP(httptest.NewRecorder(), r)
		if seen == "inyección\nen logs" || len(seen) != 32 {
			t.Errorf("id = %q", seen)
		}
	})
}

func TestRecover(t *testing.T) {
	h := Chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("algo salió mal")
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
		return NotFound("la nota no existe")
	})

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d", w.Code)
	}
	if got := decodeErrorBody(t, w.Body); got.Message != "la nota no existe" {
		t.Errorf("message = %q", got.Message)
	}
}

func TestAddLogAttrsReachesRequestLog(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))

	// Un middleware interno (como auth) agrega el user_id; la línea final de Logger debe incluirlo.
	h := Chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		AddLogAttrs(r.Context(), "user_id", 42)
		w.WriteHeader(http.StatusOK)
	}), RequestID(logger), Logger)

	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))

	if line := buf.String(); !strings.Contains(line, "msg=petición") || !strings.Contains(line, "user_id=42") {
		t.Errorf("la línea de la petición no incluye el user_id: %s", line)
	}
}

// Una respuesta que no se puede codificar es un error de programación: antes se enviaba un 200
// con el cuerpo cortado y el error quedaba en el log sin el request_id. Ahora se codifica antes
// de enviar nada, así que Recover responde un 500 correcto y lo registra con el request_id.
func TestWriteUnencodableResponse(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	h := Chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		Write(w, http.StatusOK, map[string]any{"canal": make(chan int)})
	}), RequestID(logger), Logger, Recover)

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, se esperaba 500", w.Code)
	}
	if got := decodeErrorBody(t, w.Body); got.Code != "internal_error" {
		t.Errorf("code = %q", got.Code)
	}
	logs := buf.String()
	if !strings.Contains(logs, "codificar la respuesta") || !strings.Contains(logs, "request_id="+w.Header().Get("X-Request-ID")) {
		t.Errorf("el error no quedó en el log con el request_id:\n%s", logs)
	}
}

// Un panic con la respuesta ya empezada (por ejemplo, a mitad de un stream SSE) no puede
// agregar un JSON de error al final de lo que ya se envió: se corta la conexión.
func TestRecoverAfterResponseStarted(t *testing.T) {
	var buf bytes.Buffer
	var mu sync.Mutex
	logger := slog.New(slog.NewTextHandler(&lockedWriter{w: &buf, mu: &mu}, nil))
	srv := httptest.NewServer(Chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("data: parte\n\n"))
		_ = http.NewResponseController(w).Flush()
		panic("falla a mitad del stream")
	}), RequestID(logger), Logger, Recover))
	t.Cleanup(srv.Close)

	resp, err := srv.Client().Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	body, readErr := io.ReadAll(resp.Body)
	resp.Body.Close()

	if strings.Contains(string(body), "internal_error") {
		t.Errorf("el JSON de error se mezcló con el stream: %q", body)
	}
	if readErr == nil {
		t.Error("la conexión debería cortarse en lugar de terminar la respuesta normalmente")
	}
	mu.Lock()
	logs := buf.String()
	mu.Unlock()
	if !strings.Contains(logs, "falla a mitad del stream") || !strings.Contains(logs, "request_id="+resp.Header.Get("X-Request-ID")) {
		t.Errorf("el panic no quedó en el log con el request_id:\n%s", logs)
	}
}

// lockedWriter protege el buffer del log, que escriben el handler y el servidor.
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
		// Un offset enorme hace lenta la consulta: el tope es MaxOffset.
		{"offset=10001", Page{}, []string{"offset"}},
		{"offset=-1", Page{}, []string{"offset"}},
		{"limit=diez&offset=abc", Page{}, []string{"limit", "offset"}},
	}
	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			page, err := ParsePage(httptest.NewRequest(http.MethodGet, "/notes?"+tt.query, nil))
			if tt.badFields == nil {
				if err != nil || page != tt.want {
					t.Errorf("ParsePage = %+v, %v; se esperaba %+v", page, err, tt.want)
				}
				return
			}
			var apiErr *Error
			if !errors.As(err, &apiErr) || apiErr.Status != http.StatusUnprocessableEntity {
				t.Fatalf("se esperaba un error de validación, llegó %v", err)
			}
			for _, f := range tt.badFields {
				if apiErr.Fields[f] == "" {
					t.Errorf("falta el error del campo %q: %v", f, apiErr.Fields)
				}
			}
		})
	}
}
