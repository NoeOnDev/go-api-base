package httpx

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
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
