package notes_test

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/NoeOnDev/go-api-base/internal/auth"
	"github.com/NoeOnDev/go-api-base/internal/server"
	"github.com/NoeOnDev/go-api-base/internal/testutil"
)

// These tests use the real test database (TEST_DATABASE_URL) and go through the whole API, with
// routes and middlewares, just like a real request.
// They do not use t.Parallel because each test empties the database when it starts.

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

// testAPI is the test API with a user session already started.
type testAPI struct {
	t       *testing.T
	handler http.Handler
	pool    *pgxpool.Pool
	cookie  *http.Cookie
}

func newAPI(t *testing.T) *testAPI {
	t.Helper()
	pool := testutil.NewDB(t)
	_, cookie := testutil.Login(t, pool, "alice@example.com", auth.RoleUser)
	return &testAPI{
		t:       t,
		pool:    pool,
		cookie:  cookie,
		handler: server.NewHandler(server.Deps{Logger: slog.New(slog.DiscardHandler), DB: pool}),
	}
}

// do sends a request with the user's session and returns the recorded response.
func (a *testAPI) do(method, path, body string) *httptest.ResponseRecorder {
	return a.doWith(a.cookie, method, path, body)
}

// doWith sends a request with another cookie (or without a session, with nil).
func (a *testAPI) doWith(cookie *http.Cookie, method, path, body string) *httptest.ResponseRecorder {
	a.t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	if cookie != nil {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	a.handler.ServeHTTP(w, r)
	return w
}

// decode reads the JSON body of the response and fails the test if the status is not the
// expected one.
func decode[T any](t *testing.T, w *httptest.ResponseRecorder, wantStatus int) T {
	t.Helper()
	if w.Code != wantStatus {
		t.Fatalf("status = %d, want %d; body: %s", w.Code, wantStatus, w.Body)
	}
	var v T
	if err := json.NewDecoder(w.Body).Decode(&v); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}
	return v
}

func createNote(t *testing.T, api *testAPI, title string) note {
	t.Helper()
	body := fmt.Sprintf(`{"title":%q,"body":"content"}`, title)
	return decode[note](t, api.do(http.MethodPost, "/notes", body), http.StatusCreated)
}

func TestNotesCRUD(t *testing.T) {
	api := newAPI(t)

	// Create: the title is stored without extra spaces.
	w := api.do(http.MethodPost, "/notes", `{"title":"  My note  ","body":"hello"}`)
	created := decode[note](t, w, http.StatusCreated)
	if created.ID == 0 || created.Title != "My note" || created.Body != "hello" {
		t.Fatalf("unexpected created note: %+v", created)
	}
	if loc := w.Header().Get("Location"); loc != fmt.Sprintf("/notes/%d", created.ID) {
		t.Errorf("Location = %q", loc)
	}

	path := fmt.Sprintf("/notes/%d", created.ID)

	// Get.
	got := decode[note](t, api.do(http.MethodGet, path, ""), http.StatusOK)
	if got != created {
		t.Errorf("GET = %+v, want %+v", got, created)
	}

	// Update.
	updated := decode[note](t, api.do(http.MethodPut, path, `{"title":"Edited","body":"new"}`), http.StatusOK)
	if updated.Title != "Edited" || updated.Body != "new" {
		t.Errorf("unexpected updated note: %+v", updated)
	}

	// Delete and check that it no longer exists.
	if w := api.do(http.MethodDelete, path, ""); w.Code != http.StatusNoContent {
		t.Fatalf("DELETE status = %d", w.Code)
	}
	if w := api.do(http.MethodGet, path, ""); w.Code != http.StatusNotFound {
		t.Errorf("GET after delete: status = %d", w.Code)
	}
}

func TestNotesValidation(t *testing.T) {
	api := newAPI(t)

	tests := []struct {
		name  string
		body  string
		field string
	}{
		{"empty title", `{"title":"","body":"x"}`, "title"},
		{"title with only spaces", `{"title":"   "}`, "title"},
		{"long title", fmt.Sprintf(`{"title":%q}`, strings.Repeat("a", 201)), "title"},
		{"long body", fmt.Sprintf(`{"title":"ok","body":%q}`, strings.Repeat("a", 10001)), "body"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := decode[apiError](t, api.do(http.MethodPost, "/notes", tt.body), http.StatusUnprocessableEntity)
			if resp.Error.Code != "validation_failed" || resp.Error.Fields[tt.field] == "" {
				t.Errorf("unexpected error: %+v", resp.Error)
			}
		})
	}

	t.Run("accepts exactly 200 accented characters", func(t *testing.T) {
		body := fmt.Sprintf(`{"title":%q}`, strings.Repeat("á", 200))
		decode[note](t, api.do(http.MethodPost, "/notes", body), http.StatusCreated)
	})
}

func TestNotesBadRequests(t *testing.T) {
	api := newAPI(t)

	tests := []struct {
		name, method, path, body string
		wantStatus               int
	}{
		{"malformed JSON", http.MethodPost, "/notes", `{"title":`, http.StatusBadRequest},
		{"unknown field", http.MethodPost, "/notes", `{"title":"a","admin":true}`, http.StatusBadRequest},
		{"non-numeric id", http.MethodGet, "/notes/abc", "", http.StatusBadRequest},
		{"zero id", http.MethodGet, "/notes/0", "", http.StatusBadRequest},
		{"get missing", http.MethodGet, "/notes/999", "", http.StatusNotFound},
		{"update missing", http.MethodPut, "/notes/999", `{"title":"a"}`, http.StatusNotFound},
		{"delete missing", http.MethodDelete, "/notes/999", "", http.StatusNotFound},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decode[apiError](t, api.do(tt.method, tt.path, tt.body), tt.wantStatus)
		})
	}
}

func TestNotesPagination(t *testing.T) {
	api := newAPI(t)

	for i := 1; i <= 3; i++ {
		createNote(t, api, fmt.Sprintf("note %d", i))
	}

	// The newest first.
	first := decode[noteList](t, api.do(http.MethodGet, "/notes?limit=2", ""), http.StatusOK)
	if len(first.Data) != 2 || first.Meta.Total != 3 || first.Meta.Limit != 2 {
		t.Fatalf("unexpected first page: %+v", first)
	}
	if first.Data[0].Title != "note 3" {
		t.Errorf("first item = %q, want the newest note", first.Data[0].Title)
	}

	second := decode[noteList](t, api.do(http.MethodGet, "/notes?limit=2&offset=2", ""), http.StatusOK)
	if len(second.Data) != 1 || second.Data[0].Title != "note 1" {
		t.Errorf("unexpected second page: %+v", second)
	}

	// A page out of range returns an empty list, not null.
	w := api.do(http.MethodGet, "/notes?offset=100", "")
	if !strings.Contains(w.Body.String(), `"data":[]`) {
		t.Errorf("want empty data: %s", w.Body)
	}

	resp := decode[apiError](t, api.do(http.MethodGet, "/notes?limit=0", ""), http.StatusUnprocessableEntity)
	if resp.Error.Fields["limit"] == "" {
		t.Errorf("want an error in limit: %+v", resp.Error)
	}
}

func TestNotesRequireSession(t *testing.T) {
	api := newAPI(t)

	for _, tt := range []struct{ method, path string }{
		{http.MethodGet, "/notes"},
		{http.MethodPost, "/notes"},
		{http.MethodGet, "/notes/1"},
		{http.MethodPut, "/notes/1"},
		{http.MethodDelete, "/notes/1"},
	} {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			decode[apiError](t, api.doWith(nil, tt.method, tt.path, `{"title":"x"}`), http.StatusUnauthorized)
		})
	}
}

func TestNotesBelongToTheirOwner(t *testing.T) {
	api := newAPI(t)
	alice := createNote(t, api, "Alice's note")
	path := fmt.Sprintf("/notes/%d", alice.ID)

	_, bob := testutil.Login(t, api.pool, "bob@example.com", auth.RoleUser)

	// Bob does not see Alice's note in his list…
	list := decode[noteList](t, api.doWith(bob, http.MethodGet, "/notes", ""), http.StatusOK)
	if len(list.Data) != 0 || list.Meta.Total != 0 {
		t.Errorf("Bob sees other users' notes: %+v", list)
	}

	// …nor can he read, edit or delete it: it responds 404, as if it did not exist.
	for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodDelete} {
		t.Run(method, func(t *testing.T) {
			decode[apiError](t, api.doWith(bob, method, path, `{"title":"hacked"}`), http.StatusNotFound)
		})
	}

	// Alice's note is untouched.
	got := decode[note](t, api.do(http.MethodGet, path, ""), http.StatusOK)
	if got.Title != "Alice's note" {
		t.Errorf("the note changed: %+v", got)
	}
}
