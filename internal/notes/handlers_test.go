package notes_test

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/NoeOnDev/go-api-base/internal/server"
	"github.com/NoeOnDev/go-api-base/internal/testutil"
)

// Estos tests usan la base de pruebas real (TEST_DATABASE_URL) y recorren la API completa,
// con rutas y middlewares, igual que una petición de verdad.
// No usan t.Parallel porque cada test vacía la tabla notes al empezar.

type note struct {
	ID    int64  `json:"id"`
	Title string `json:"title"`
	Body  string `json:"body"`
}

type apiError struct {
	Error struct {
		Code   string            `json:"code"`
		Fields map[string]string `json:"fields"`
	} `json:"error"`
}

type noteList struct {
	Data []note `json:"data"`
	Meta struct {
		Limit  int   `json:"limit"`
		Offset int   `json:"offset"`
		Total  int64 `json:"total"`
	} `json:"meta"`
}

func newAPI(t *testing.T) http.Handler {
	t.Helper()
	pool := testutil.NewDB(t, "notes")
	return server.NewHandler(server.Deps{
		Logger: slog.New(slog.DiscardHandler),
		DB:     pool,
	})
}

// do envía una petición a la API y devuelve la respuesta grabada.
func do(t *testing.T, api http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	api.ServeHTTP(w, r)
	return w
}

// decode lee el cuerpo JSON de la respuesta y falla el test si el status no es el esperado.
func decode[T any](t *testing.T, w *httptest.ResponseRecorder, wantStatus int) T {
	t.Helper()
	if w.Code != wantStatus {
		t.Fatalf("status = %d, se esperaba %d; cuerpo: %s", w.Code, wantStatus, w.Body)
	}
	var v T
	if err := json.NewDecoder(w.Body).Decode(&v); err != nil {
		t.Fatalf("respuesta no es JSON válido: %v", err)
	}
	return v
}

func createNote(t *testing.T, api http.Handler, title string) note {
	t.Helper()
	body := fmt.Sprintf(`{"title":%q,"body":"contenido"}`, title)
	return decode[note](t, do(t, api, http.MethodPost, "/notes", body), http.StatusCreated)
}

func TestNotesCRUD(t *testing.T) {
	api := newAPI(t)

	// Crear: el título se guarda sin espacios sobrantes.
	w := do(t, api, http.MethodPost, "/notes", `{"title":"  Mi nota  ","body":"hola"}`)
	created := decode[note](t, w, http.StatusCreated)
	if created.ID == 0 || created.Title != "Mi nota" || created.Body != "hola" {
		t.Fatalf("nota creada inesperada: %+v", created)
	}
	if loc := w.Header().Get("Location"); loc != fmt.Sprintf("/notes/%d", created.ID) {
		t.Errorf("Location = %q", loc)
	}

	path := fmt.Sprintf("/notes/%d", created.ID)

	// Ver.
	got := decode[note](t, do(t, api, http.MethodGet, path, ""), http.StatusOK)
	if got != created {
		t.Errorf("GET = %+v, se esperaba %+v", got, created)
	}

	// Editar.
	updated := decode[note](t, do(t, api, http.MethodPut, path, `{"title":"Editada","body":"nuevo"}`), http.StatusOK)
	if updated.Title != "Editada" || updated.Body != "nuevo" {
		t.Errorf("nota editada inesperada: %+v", updated)
	}

	// Borrar y comprobar que ya no existe.
	if w := do(t, api, http.MethodDelete, path, ""); w.Code != http.StatusNoContent {
		t.Fatalf("DELETE status = %d", w.Code)
	}
	if w := do(t, api, http.MethodGet, path, ""); w.Code != http.StatusNotFound {
		t.Errorf("GET después de borrar: status = %d", w.Code)
	}
}

func TestNotesValidation(t *testing.T) {
	api := newAPI(t)

	tests := []struct {
		name  string
		body  string
		field string
	}{
		{"título vacío", `{"title":"","body":"x"}`, "title"},
		{"título solo espacios", `{"title":"   "}`, "title"},
		{"título largo", fmt.Sprintf(`{"title":%q}`, strings.Repeat("a", 201)), "title"},
		{"cuerpo largo", fmt.Sprintf(`{"title":"ok","body":%q}`, strings.Repeat("a", 10001)), "body"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := decode[apiError](t, do(t, api, http.MethodPost, "/notes", tt.body), http.StatusUnprocessableEntity)
			if resp.Error.Code != "validation_failed" || resp.Error.Fields[tt.field] == "" {
				t.Errorf("error inesperado: %+v", resp.Error)
			}
		})
	}

	t.Run("acepta exactamente 200 caracteres con acentos", func(t *testing.T) {
		body := fmt.Sprintf(`{"title":%q}`, strings.Repeat("á", 200))
		decode[note](t, do(t, api, http.MethodPost, "/notes", body), http.StatusCreated)
	})
}

func TestNotesBadRequests(t *testing.T) {
	api := newAPI(t)

	tests := []struct {
		name, method, path, body string
		wantStatus               int
	}{
		{"JSON mal formado", http.MethodPost, "/notes", `{"title":`, http.StatusBadRequest},
		{"campo desconocido", http.MethodPost, "/notes", `{"title":"a","admin":true}`, http.StatusBadRequest},
		{"id no numérico", http.MethodGet, "/notes/abc", "", http.StatusBadRequest},
		{"id cero", http.MethodGet, "/notes/0", "", http.StatusBadRequest},
		{"ver inexistente", http.MethodGet, "/notes/999", "", http.StatusNotFound},
		{"editar inexistente", http.MethodPut, "/notes/999", `{"title":"a"}`, http.StatusNotFound},
		{"borrar inexistente", http.MethodDelete, "/notes/999", "", http.StatusNotFound},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decode[apiError](t, do(t, api, tt.method, tt.path, tt.body), tt.wantStatus)
		})
	}
}

func TestNotesPagination(t *testing.T) {
	api := newAPI(t)

	for i := 1; i <= 3; i++ {
		createNote(t, api, fmt.Sprintf("nota %d", i))
	}

	// Las más nuevas primero.
	first := decode[noteList](t, do(t, api, http.MethodGet, "/notes?limit=2", ""), http.StatusOK)
	if len(first.Data) != 2 || first.Meta.Total != 3 || first.Meta.Limit != 2 {
		t.Fatalf("primera página inesperada: %+v", first)
	}
	if first.Data[0].Title != "nota 3" {
		t.Errorf("primer elemento = %q, se esperaba la nota más nueva", first.Data[0].Title)
	}

	second := decode[noteList](t, do(t, api, http.MethodGet, "/notes?limit=2&offset=2", ""), http.StatusOK)
	if len(second.Data) != 1 || second.Data[0].Title != "nota 1" {
		t.Errorf("segunda página inesperada: %+v", second)
	}

	// Una página fuera de rango devuelve una lista vacía, no null.
	w := do(t, api, http.MethodGet, "/notes?offset=100", "")
	if !strings.Contains(w.Body.String(), `"data":[]`) {
		t.Errorf("se esperaba data vacía: %s", w.Body)
	}

	resp := decode[apiError](t, do(t, api, http.MethodGet, "/notes?limit=0", ""), http.StatusUnprocessableEntity)
	if resp.Error.Fields["limit"] == "" {
		t.Errorf("se esperaba error en limit: %+v", resp.Error)
	}
}
