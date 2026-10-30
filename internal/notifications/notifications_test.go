package notifications_test

import (
	"cmp"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/NoeOnDev/go-api-base/internal/auth"
	"github.com/NoeOnDev/go-api-base/internal/notifications"
	"github.com/NoeOnDev/go-api-base/internal/realtime"
	"github.com/NoeOnDev/go-api-base/internal/store"
	"github.com/NoeOnDev/go-api-base/internal/testutil"
)

type listResponse struct {
	Data []notifications.Response `json:"data"`
	Meta struct {
		Limit int32   `json:"limit"`
		Next  *string `json:"next"`
	} `json:"meta"`
	Unread int64 `json:"unread"`
}

// The list goes by cursor: following meta.next it goes through every notification once, from
// the newest, and the last page has next null.
func TestNotificationsPagination(t *testing.T) {
	pool := testutil.NewDB(t)
	svc := notifications.NewService(store.New(pool), realtime.NewBroker(), nil)
	api := testutil.NewAPI(pool)
	alice, cookie := testutil.Login(t, pool, "alice@example.com", auth.RoleUser)
	for i := range 5 {
		if err := svc.Notify(t.Context(), alice.ID, "task.assigned", map[string]int{"task_id": i + 1}); err != nil {
			t.Fatal(err)
		}
	}

	var ids []int64
	path := "/notifications?limit=2"
	for pages := 0; ; pages++ {
		if pages > 5 {
			t.Fatal("the pages do not end")
		}
		page := testutil.Decode[listResponse](t, testutil.Do(t, api, cookie, http.MethodGet, path, ""), http.StatusOK)
		for _, n := range page.Data {
			ids = append(ids, n.ID)
		}
		if page.Meta.Next == nil {
			break
		}
		path = "/notifications?limit=2&after=" + *page.Meta.Next
	}
	if len(ids) != 5 || !slices.IsSortedFunc(ids, func(a, b int64) int { return cmp.Compare(b, a) }) {
		t.Errorf("ids = %v, want the 5, from the newest", ids)
	}

	// After marking the newest one as read, the unread ones are 4, also by cursor.
	testutil.Do(t, api, cookie, http.MethodPost, fmt.Sprintf("/notifications/%d/read", ids[0]), "")
	unread := testutil.Decode[listResponse](t, testutil.Do(t, api, cookie, http.MethodGet, "/notifications?unread=true&limit=3", ""), http.StatusOK)
	if len(unread.Data) != 3 || unread.Data[0].ID != ids[1] || unread.Meta.Next == nil || unread.Unread != 4 {
		t.Errorf("unread, first page: %+v", unread)
	}

	for _, query := range []string{"?after=not-a-cursor", "?after=" + strings.Repeat("a", 201), "?limit=0"} {
		testutil.Decode[testutil.APIError](t, testutil.Do(t, api, cookie, http.MethodGet, "/notifications"+query, ""), http.StatusUnprocessableEntity)
	}
}

func TestNotificationsOwnership(t *testing.T) {
	pool := testutil.NewDB(t)
	broker := realtime.NewBroker()
	svc := notifications.NewService(store.New(pool), broker, nil)
	api := testutil.NewAPI(pool, testutil.WithBroker(broker))
	alice, aliceCookie := testutil.Login(t, pool, "alice@example.com", auth.RoleUser)
	bob, bobCookie := testutil.Login(t, pool, "bob@example.com", auth.RoleUser)

	for i := range 2 {
		if err := svc.Notify(t.Context(), alice.ID, "task.assigned", map[string]int{"task_id": i + 1}); err != nil {
			t.Fatal(err)
		}
	}
	if err := svc.Notify(t.Context(), bob.ID, "task.assigned", map[string]int{"task_id": 9}); err != nil {
		t.Fatal(err)
	}

	// Each one sees only their own, from the newest to the oldest.
	list := testutil.Decode[listResponse](t, testutil.Do(t, api, aliceCookie, http.MethodGet, "/notifications", ""), http.StatusOK)
	if len(list.Data) != 2 || list.Meta.Next != nil || list.Unread != 2 || string(list.Data[0].Data) != `{"task_id":2}` {
		t.Fatalf("Alice's list: %+v", list)
	}
	bobList := testutil.Decode[listResponse](t, testutil.Do(t, api, bobCookie, http.MethodGet, "/notifications", ""), http.StatusOK)
	if len(bobList.Data) != 1 {
		t.Fatalf("Bob's list: %+v", bobList)
	}

	// Another user's answers 404, as in notes.
	path := fmt.Sprintf("/notifications/%d/read", bobList.Data[0].ID)
	testutil.Decode[testutil.APIError](t, testutil.Do(t, api, aliceCookie, http.MethodPost, path, ""), http.StatusNotFound)

	// Mark one as read.
	path = fmt.Sprintf("/notifications/%d/read", list.Data[0].ID)
	read := testutil.Decode[notifications.Response](t, testutil.Do(t, api, aliceCookie, http.MethodPost, path, ""), http.StatusOK)
	if read.ReadAt == nil {
		t.Error("read_at should have the date")
	}
	unread := testutil.Decode[listResponse](t, testutil.Do(t, api, aliceCookie, http.MethodGet, "/notifications?unread=true", ""), http.StatusOK)
	if len(unread.Data) != 1 || unread.Unread != 1 {
		t.Errorf("unread: %+v", unread)
	}

	// Mark all of them.
	if w := testutil.Do(t, api, aliceCookie, http.MethodPost, "/notifications/read-all", ""); w.Code != http.StatusNoContent {
		t.Fatalf("read-all: status = %d", w.Code)
	}
	if l := testutil.Decode[listResponse](t, testutil.Do(t, api, aliceCookie, http.MethodGet, "/notifications", ""), http.StatusOK); l.Unread != 0 {
		t.Errorf("after read-all %d are still unread", l.Unread)
	}
	// Bob's were left alone.
	if l := testutil.Decode[listResponse](t, testutil.Do(t, api, bobCookie, http.MethodGet, "/notifications", ""), http.StatusOK); l.Unread != 1 {
		t.Errorf("Bob's should not change: %+v", l)
	}

	// Errores.
	testutil.Decode[testutil.APIError](t, testutil.Do(t, api, aliceCookie, http.MethodGet, "/notifications?unread=yes", ""), http.StatusUnprocessableEntity)
	testutil.Decode[testutil.APIError](t, testutil.Do(t, api, nil, http.MethodGet, "/notifications", ""), http.StatusUnauthorized)
	if w := testutil.Do(t, api, aliceCookie, http.MethodGet, "/notifications", ""); w.Header().Get("Cache-Control") != "no-store" {
		t.Error("notifications should not be cached")
	}
}

// do sends a request to the real server with the session.
func do(t *testing.T, srv *httptest.Server, cookie *http.Cookie, method, path, body string) *http.Response {
	t.Helper()
	req, _ := http.NewRequestWithContext(t.Context(), method, srv.URL+path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(cookie)
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func TestNotificationsArriveLive(t *testing.T) {
	pool := testutil.NewDB(t)
	broker := realtime.NewBroker()
	svc := notifications.NewService(store.New(pool), broker, nil)
	srv := testutil.NewServer(t, pool, testutil.WithBroker(broker))
	alice, aliceCookie := testutil.Login(t, pool, "alice@example.com", auth.RoleUser)
	_, bobCookie := testutil.Login(t, pool, "bob@example.com", auth.RoleUser)

	aliceEvents := testutil.OpenEvents(t, srv, aliceCookie, "")
	bobEvents := testutil.OpenEvents(t, srv, bobCookie, "")

	if err := svc.Notify(t.Context(), alice.ID, "task.assigned", map[string]int{"task_id": 1}); err != nil {
		t.Fatal(err)
	}
	ev := aliceEvents.Next(t, 2*time.Second)
	var n notifications.Response
	if err := json.Unmarshal([]byte(ev.Data), &n); err != nil || ev.Type != notifications.EventNotification || n.Kind != "task.assigned" {
		t.Fatalf("event: %+v (%v)", ev, err)
	}
	bobEvents.Nothing(t, 100*time.Millisecond)

	// Marking it as read tells the other tabs, to update the counter.
	if resp := do(t, srv, aliceCookie, http.MethodPost, fmt.Sprintf("/notifications/%d/read", n.ID), ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("read: status = %d", resp.StatusCode)
	}
	if ev := aliceEvents.Next(t, 2*time.Second); ev.Type != notifications.EventRead || ev.Data != fmt.Sprintf(`{"id":%d}`, n.ID) {
		t.Errorf("read event: %+v", ev)
	}
}

func TestSecurityChangesNotify(t *testing.T) {
	pool := testutil.NewDB(t)
	srv := testutil.NewServer(t, pool)
	_, cookie := testutil.Login(t, pool, "alice@example.com", auth.RoleUser)
	events := testutil.OpenEvents(t, srv, cookie, "")

	// Changing the password from the profile notifies live and is stored.
	body := `{"current_password":"test-password","password":"a-new-password"}`
	if resp := do(t, srv, cookie, http.MethodPut, "/auth/password", body); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("password change: status = %d", resp.StatusCode)
	}
	ev := events.Next(t, 2*time.Second)
	if ev.Type != notifications.EventNotification || !strings.Contains(ev.Data, auth.NotifyPasswordChanged) || !strings.Contains(ev.Data, "password was changed") {
		t.Errorf("event: %+v", ev)
	}

	resp := do(t, srv, cookie, http.MethodGet, "/notifications", "")
	var list listResponse
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		t.Fatal(err)
	}
	// The text is built on read (it is not stored): no message is left in data.
	if len(list.Data) != 1 || list.Data[0].Kind != auth.NotifyPasswordChanged ||
		!strings.Contains(list.Data[0].Message, "password was changed") || string(list.Data[0].Data) != "{}" {
		t.Errorf("stored notifications: %+v", list)
	}
}

func TestTwoFactorChangesNotify(t *testing.T) {
	pool := testutil.NewDB(t)
	api := testutil.NewAPI(pool)
	_, cookie := testutil.Login(t, pool, "alice@example.com", auth.RoleUser)

	tf := testutil.EnableTwoFactor(t, api, cookie, testutil.Password)
	body := fmt.Sprintf(`{"password":%q,"code":%q}`, testutil.Password, tf.Code(t))
	if w := testutil.Do(t, api, cookie, http.MethodPost, "/auth/two-factor/disable", body); w.Code != http.StatusNoContent {
		t.Fatalf("disable: status = %d", w.Code)
	}

	list := testutil.Decode[listResponse](t, testutil.Do(t, api, cookie, http.MethodGet, "/notifications", ""), http.StatusOK)
	if len(list.Data) != 2 || list.Data[0].Kind != auth.NotifyTwoFactorDisabled || list.Data[1].Kind != auth.NotifyTwoFactorEnabled {
		t.Errorf("notifications: %+v", list.Data)
	}
}
