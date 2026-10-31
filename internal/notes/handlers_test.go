package notes_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/NoeOnDev/go-api-base/internal/auth"
	"github.com/NoeOnDev/go-api-base/internal/testutil"
)

// These tests use the real test database (TEST_DATABASE_URL) and go through the whole API, with
// routes and middlewares, just like a real request.
// They do not use t.Parallel because each test empties the database when it starts.

type note struct {
	ID    int64  `json:"id"`
	Title string `json:"title"`
	Body  string `json:"body"`
	// The dates as the API sends them, to check their format.
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

type noteList struct {
	Data []note `json:"data"`
	Meta struct {
		Limit  int   `json:"limit"`
		Offset int   `json:"offset"`
		Total  int64 `json:"total"`
	} `json:"meta"`
}

// newAPI empties the database and returns the API and the session of a user, Alice.
func newAPI(t *testing.T) (http.Handler, *pgxpool.Pool, *http.Cookie) {
	t.Helper()
	pool := testutil.NewDB(t)
	_, alice := testutil.Login(t, pool, "alice@example.com", auth.RoleUser)
	return testutil.NewAPI(pool), pool, alice
}

func createNote(t *testing.T, api http.Handler, cookie *http.Cookie, title string) note {
	t.Helper()
	body := fmt.Sprintf(`{"title":%q,"body":"content"}`, title)
	return testutil.Decode[note](t, testutil.Do(t, api, cookie, http.MethodPost, "/notes", body), http.StatusCreated)
}

func TestNotesCRUD(t *testing.T) {
	api, _, alice := newAPI(t)

	// Create: the title is stored without extra spaces.
	w := testutil.Do(t, api, alice, http.MethodPost, "/notes", `{"title":"  My note  ","body":"hello"}`)
	created := testutil.Decode[note](t, w, http.StatusCreated)
	if created.ID == 0 || created.Title != "My note" || created.Body != "hello" {
		t.Fatalf("unexpected created note: %+v", created)
	}
	// The dates go in UTC, whatever the zone of the server (see "Time zones" in docs/decisions.md).
	if !strings.HasSuffix(created.CreatedAt, "Z") || !strings.HasSuffix(created.UpdatedAt, "Z") {
		t.Errorf("dates = %q and %q, want them in UTC (with Z)", created.CreatedAt, created.UpdatedAt)
	}
	if loc := w.Header().Get("Location"); loc != fmt.Sprintf("/notes/%d", created.ID) {
		t.Errorf("Location = %q", loc)
	}

	path := fmt.Sprintf("/notes/%d", created.ID)

	// Get.
	got := testutil.Decode[note](t, testutil.Do(t, api, alice, http.MethodGet, path, ""), http.StatusOK)
	if got != created {
		t.Errorf("GET = %+v, want %+v", got, created)
	}

	// Update.
	w = testutil.Do(t, api, alice, http.MethodPut, path, `{"title":"Edited","body":"new"}`)
	updated := testutil.Decode[note](t, w, http.StatusOK)
	if updated.Title != "Edited" || updated.Body != "new" {
		t.Errorf("unexpected updated note: %+v", updated)
	}

	// Delete and check that it no longer exists.
	if w := testutil.Do(t, api, alice, http.MethodDelete, path, ""); w.Code != http.StatusNoContent {
		t.Fatalf("DELETE status = %d", w.Code)
	}
	if w := testutil.Do(t, api, alice, http.MethodGet, path, ""); w.Code != http.StatusNotFound {
		t.Errorf("GET after delete: status = %d", w.Code)
	}
}

func TestNotesValidation(t *testing.T) {
	api, _, alice := newAPI(t)

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
			w := testutil.Do(t, api, alice, http.MethodPost, "/notes", tt.body)
			resp := testutil.Decode[testutil.APIError](t, w, http.StatusUnprocessableEntity)
			if resp.Error.Code != "validation_failed" || resp.Error.Fields[tt.field] == "" {
				t.Errorf("unexpected error: %+v", resp.Error)
			}
		})
	}

	t.Run("accepts exactly 200 accented characters", func(t *testing.T) {
		body := fmt.Sprintf(`{"title":%q}`, strings.Repeat("á", 200))
		testutil.Decode[note](t, testutil.Do(t, api, alice, http.MethodPost, "/notes", body), http.StatusCreated)
	})
}

func TestNotesBadRequests(t *testing.T) {
	api, _, alice := newAPI(t)

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
			testutil.Decode[testutil.APIError](t, testutil.Do(t, api, alice, tt.method, tt.path, tt.body), tt.wantStatus)
		})
	}
}

func TestNotesPagination(t *testing.T) {
	api, _, alice := newAPI(t)

	for i := 1; i <= 3; i++ {
		createNote(t, api, alice, fmt.Sprintf("note %d", i))
	}

	// The newest first.
	first := testutil.Decode[noteList](t, testutil.Do(t, api, alice, http.MethodGet, "/notes?limit=2", ""), http.StatusOK)
	if len(first.Data) != 2 || first.Meta.Total != 3 || first.Meta.Limit != 2 {
		t.Fatalf("unexpected first page: %+v", first)
	}
	if first.Data[0].Title != "note 3" {
		t.Errorf("first item = %q, want the newest note", first.Data[0].Title)
	}

	second := testutil.Decode[noteList](t, testutil.Do(t, api, alice, http.MethodGet, "/notes?limit=2&offset=2", ""), http.StatusOK)
	if len(second.Data) != 1 || second.Data[0].Title != "note 1" {
		t.Errorf("unexpected second page: %+v", second)
	}

	// A page out of range returns an empty list, not null.
	w := testutil.Do(t, api, alice, http.MethodGet, "/notes?offset=100", "")
	if !strings.Contains(w.Body.String(), `"data":[]`) {
		t.Errorf("want empty data: %s", w.Body)
	}

	w = testutil.Do(t, api, alice, http.MethodGet, "/notes?limit=0", "")
	resp := testutil.Decode[testutil.APIError](t, w, http.StatusUnprocessableEntity)
	if resp.Error.Fields["limit"] == "" {
		t.Errorf("want an error in limit: %+v", resp.Error)
	}
}

func TestNotesRequireSession(t *testing.T) {
	api, _, _ := newAPI(t)

	for _, tt := range []struct{ method, path string }{
		{http.MethodGet, "/notes"},
		{http.MethodPost, "/notes"},
		{http.MethodGet, "/notes/1"},
		{http.MethodPut, "/notes/1"},
		{http.MethodDelete, "/notes/1"},
	} {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			testutil.Decode[testutil.APIError](t, testutil.Do(t, api, nil, tt.method, tt.path, `{"title":"x"}`), http.StatusUnauthorized)
		})
	}
}

func TestNotesBelongToTheirOwner(t *testing.T) {
	api, pool, alice := newAPI(t)
	aliceNote := createNote(t, api, alice, "Alice's note")
	path := fmt.Sprintf("/notes/%d", aliceNote.ID)

	_, bob := testutil.Login(t, pool, "bob@example.com", auth.RoleUser)

	// Bob does not see Alice's note in his list…
	list := testutil.Decode[noteList](t, testutil.Do(t, api, bob, http.MethodGet, "/notes", ""), http.StatusOK)
	if len(list.Data) != 0 || list.Meta.Total != 0 {
		t.Errorf("Bob sees other users' notes: %+v", list)
	}

	// …nor can he read, edit or delete it: it responds 404, as if it did not exist.
	for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodDelete} {
		t.Run(method, func(t *testing.T) {
			testutil.Decode[testutil.APIError](t, testutil.Do(t, api, bob, method, path, `{"title":"hacked"}`), http.StatusNotFound)
		})
	}

	// Alice's note is untouched.
	got := testutil.Decode[note](t, testutil.Do(t, api, alice, http.MethodGet, path, ""), http.StatusOK)
	if got.Title != "Alice's note" {
		t.Errorf("the note changed: %+v", got)
	}
}
