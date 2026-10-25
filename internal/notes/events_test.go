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

// Otra pestaña del mismo usuario ve los cambios en vivo; la de otro usuario, no.
func TestNoteChangesArriveLive(t *testing.T) {
	pool := testutil.NewDB(t)
	srv := testutil.NewServer(t, pool)
	_, ana := testutil.Login(t, pool, "ana@ejemplo.com", auth.RoleUser)
	_, luis := testutil.Login(t, pool, "luis@ejemplo.com", auth.RoleUser)

	otherTab := testutil.OpenEvents(t, srv, ana, "")
	luisTab := testutil.OpenEvents(t, srv, luis, "")

	send := func(method, path, body string) *http.Response {
		t.Helper()
		req, _ := http.NewRequestWithContext(t.Context(), method, srv.URL+path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(ana)
		resp, err := srv.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { resp.Body.Close() })
		return resp
	}

	resp := send(http.MethodPost, "/notes", `{"title":"Comprar pan"}`)
	var created struct{ ID int64 }
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil || resp.StatusCode != http.StatusCreated {
		t.Fatalf("crear: status %d (%v)", resp.StatusCode, err)
	}
	if ev := otherTab.Next(t, 2*time.Second); ev.Type != notes.EventCreated || !strings.Contains(ev.Data, "Comprar pan") {
		t.Errorf("creada: %+v", ev)
	}

	id := strconv.FormatInt(created.ID, 10)
	path := "/notes/" + id
	send(http.MethodPut, path, `{"title":"Comprar pan y leche"}`)
	if ev := otherTab.Next(t, 2*time.Second); ev.Type != notes.EventUpdated || !strings.Contains(ev.Data, "leche") {
		t.Errorf("actualizada: %+v", ev)
	}

	send(http.MethodDelete, path, "")
	if ev := otherTab.Next(t, 2*time.Second); ev.Type != notes.EventDeleted || ev.Data != `{"id":`+id+`}` {
		t.Errorf("borrada: %+v", ev)
	}

	luisTab.Nothing(t, 100*time.Millisecond)
}
