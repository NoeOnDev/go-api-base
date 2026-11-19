package notes_test

import (
	"bytes"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/NoeOnDev/go-api-base/internal/auth"
	"github.com/NoeOnDev/go-api-base/internal/files"
	"github.com/NoeOnDev/go-api-base/internal/storage"
	"github.com/NoeOnDev/go-api-base/internal/store"
	"github.com/NoeOnDev/go-api-base/internal/testutil"
)

type attachment struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	ContentType string `json:"content_type"`
	URL         string `json:"url"`
}

type attachmentList struct {
	Data []attachment `json:"data"`
}

func TestNoteAttachments(t *testing.T) {
	pool := testutil.NewDB(t)
	st := storage.NewMemory()
	api := testutil.NewAPI(pool, testutil.WithStorage(st))
	_, alice := testutil.Login(t, pool, "alice@example.com", auth.RoleUser)
	_, bob := testutil.Login(t, pool, "bob@example.com", auth.RoleUser)

	n := testutil.Decode[note](t, testutil.Do(t, api, alice, http.MethodPost, "/notes", `{"title":"Trip"}`), http.StatusCreated)
	path := fmt.Sprintf("/notes/%d/attachments", n.ID)

	// Attaching stores the file and adds it to the note.
	a := testutil.Decode[attachment](t, testutil.Upload(t, api, alice, path, "ticket.pdf", testutil.PDF), http.StatusCreated)
	b := testutil.Decode[attachment](t, testutil.Upload(t, api, alice, path, "map.png", testutil.PNG), http.StatusCreated)
	if a.Name != "ticket.pdf" || a.ContentType != "application/pdf" || a.URL != fmt.Sprintf("/files/%d", a.ID) {
		t.Errorf("attachment = %+v", a)
	}
	list := testutil.Decode[attachmentList](t, testutil.Do(t, api, alice, http.MethodGet, path, ""), http.StatusOK)
	if len(list.Data) != 2 || list.Data[0].ID != a.ID || list.Data[1].ID != b.ID {
		t.Errorf("attachments = %+v, want the two in the order they were added", list.Data)
	}
	if w := testutil.Do(t, api, alice, http.MethodGet, a.URL, ""); w.Code != http.StatusOK || !bytes.Equal(w.Body.Bytes(), testutil.PDF) {
		t.Errorf("download: status = %d", w.Code)
	}

	// What the note does not accept.
	for name, tt := range map[string]struct {
		file    string
		content []byte
		want    string
	}{
		"a page":              {"page.html", []byte("<html></html>"), "not allowed"},
		"more than 5 MB":      {"big.png", append(bytes.Clone(testutil.PNG), make([]byte, 5<<20)...), "5 MB"},
		"a file with nothing": {"empty.pdf", nil, "required"},
	} {
		w := testutil.Upload(t, api, alice, path, tt.file, tt.content)
		if resp := testutil.Decode[testutil.APIError](t, w, http.StatusUnprocessableEntity); !strings.Contains(resp.Error.Fields["file"], tt.want) {
			t.Errorf("%s: fields = %v, want %q on file", name, resp.Error.Fields, tt.want)
		}
	}

	// Someone else's note answers 404 on every route, and its files cannot be downloaded.
	testutil.Decode[testutil.APIError](t, testutil.Upload(t, api, bob, path, "x.png", testutil.PNG), http.StatusNotFound)
	testutil.Decode[testutil.APIError](t, testutil.Do(t, api, bob, http.MethodGet, path, ""), http.StatusNotFound)
	testutil.Decode[testutil.APIError](t, testutil.Do(t, api, bob, http.MethodDelete, fmt.Sprintf("%s/%d", path, a.ID), ""), http.StatusNotFound)
	testutil.Decode[testutil.APIError](t, testutil.Do(t, api, bob, http.MethodGet, a.URL, ""), http.StatusNotFound)
	testutil.Decode[testutil.APIError](t, testutil.Upload(t, api, nil, path, "x.png", testutil.PNG), http.StatusUnauthorized)
	testutil.Decode[testutil.APIError](t, testutil.Upload(t, api, alice, "/notes/999999/attachments", "x.png", testutil.PNG), http.StatusNotFound)
	if len(st.Keys()) != 2 {
		t.Errorf("%d objects in the store, want the 2 attached", len(st.Keys()))
	}

	// Taking one off deletes it: it is no longer listed nor served, and the cleanup deletes its bytes.
	if w := testutil.Do(t, api, alice, http.MethodDelete, fmt.Sprintf("%s/%d", path, a.ID), ""); w.Code != http.StatusNoContent {
		t.Fatalf("detach: status = %d; body: %s", w.Code, w.Body)
	}
	testutil.Decode[testutil.APIError](t, testutil.Do(t, api, alice, http.MethodDelete, fmt.Sprintf("%s/%d", path, a.ID), ""), http.StatusNotFound)
	testutil.Decode[testutil.APIError](t, testutil.Do(t, api, alice, http.MethodGet, a.URL, ""), http.StatusNotFound)
	list = testutil.Decode[attachmentList](t, testutil.Do(t, api, alice, http.MethodGet, path, ""), http.StatusOK)
	if len(list.Data) != 1 || list.Data[0].ID != b.ID {
		t.Errorf("attachments after taking one off = %+v", list.Data)
	}

	// Deleting the note releases the ones it still had.
	if w := testutil.Do(t, api, alice, http.MethodDelete, fmt.Sprintf("/notes/%d", n.ID), ""); w.Code != http.StatusNoContent {
		t.Fatalf("delete the note: status = %d", w.Code)
	}
	testutil.Decode[testutil.APIError](t, testutil.Do(t, api, alice, http.MethodGet, b.URL, ""), http.StatusNotFound)

	if deleted, err := files.Purge(t.Context(), store.New(pool), st); err != nil || deleted != 2 {
		t.Errorf("Purge = %d, %v; want the 2 files", deleted, err)
	}
	if len(st.Keys()) != 0 {
		t.Errorf("%d objects left in the store", len(st.Keys()))
	}
}

// Someone else's note is not deleted, and its attachments stay.
func TestDeleteSomeoneElsesNoteKeepsItsAttachments(t *testing.T) {
	pool := testutil.NewDB(t)
	api := testutil.NewAPI(pool)
	_, alice := testutil.Login(t, pool, "alice@example.com", auth.RoleUser)
	_, bob := testutil.Login(t, pool, "bob@example.com", auth.RoleUser)

	n := testutil.Decode[note](t, testutil.Do(t, api, alice, http.MethodPost, "/notes", `{"title":"Trip"}`), http.StatusCreated)
	a := testutil.Decode[attachment](t, testutil.Upload(t, api, alice, fmt.Sprintf("/notes/%d/attachments", n.ID), "map.png", testutil.PNG), http.StatusCreated)

	testutil.Decode[testutil.APIError](t, testutil.Do(t, api, bob, http.MethodDelete, fmt.Sprintf("/notes/%d", n.ID), ""), http.StatusNotFound)
	if w := testutil.Do(t, api, alice, http.MethodGet, a.URL, ""); w.Code != http.StatusOK {
		t.Errorf("the attachment after someone else tried to delete the note: status = %d", w.Code)
	}
}
