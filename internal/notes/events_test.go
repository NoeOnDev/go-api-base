package notes_test

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/NoeOnDev/go-api-base/internal/auth"
	"github.com/NoeOnDev/go-api-base/internal/notes"
	"github.com/NoeOnDev/go-api-base/internal/testutil"
)

// Another tab of the same user sees the changes live; another user's does not.
func TestNoteChangesArriveLive(t *testing.T) {
	pool := testutil.NewDB(t)
	srv := testutil.NewServer(t, pool)
	_, alice := testutil.Login(t, pool, "alice@example.com", auth.RoleUser)
	_, bob := testutil.Login(t, pool, "bob@example.com", auth.RoleUser)

	otherTab := testutil.OpenEvents(t, srv, alice, "")
	bobTab := testutil.OpenEvents(t, srv, bob, "")

	send := func(method, path, body string) *http.Response {
		t.Helper()
		req, _ := http.NewRequestWithContext(t.Context(), method, srv.URL+path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(alice)
		resp, err := srv.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { resp.Body.Close() })
		return resp
	}

	resp := send(http.MethodPost, "/notes", `{"title":"Buy bread"}`)
	var created struct{ ID int64 }
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil || resp.StatusCode != http.StatusCreated {
		t.Fatalf("create: status %d (%v)", resp.StatusCode, err)
	}
	if ev := otherTab.Next(t, 2*time.Second); ev.Type != notes.EventCreated || !strings.Contains(ev.Data, "Buy bread") {
		t.Errorf("created: %+v", ev)
	}

	id := strconv.FormatInt(created.ID, 10)
	path := "/notes/" + id
	send(http.MethodPut, path, `{"title":"Buy bread and milk"}`)
	if ev := otherTab.Next(t, 2*time.Second); ev.Type != notes.EventUpdated || !strings.Contains(ev.Data, "milk") {
		t.Errorf("updated: %+v", ev)
	}

	send(http.MethodDelete, path, "")
	if ev := otherTab.Next(t, 2*time.Second); ev.Type != notes.EventDeleted || ev.Data != `{"id":`+id+`}` {
		t.Errorf("deleted: %+v", ev)
	}

	bobTab.Nothing(t, 100*time.Millisecond)
}
