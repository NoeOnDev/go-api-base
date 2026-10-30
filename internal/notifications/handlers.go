package notifications

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/jackc/pgx/v5"

	"github.com/NoeOnDev/go-api-base/internal/auth"
	"github.com/NoeOnDev/go-api-base/internal/httpx"
	"github.com/NoeOnDev/go-api-base/internal/i18n"
	"github.com/NoeOnDev/go-api-base/internal/realtime"
	"github.com/NoeOnDev/go-api-base/internal/store"
)

var errNotFound = httpx.NotFound("notification_not_found")

// listResponse is the page of the list, with the unread count for the frontend's counter.
type listResponse struct {
	httpx.CursorList[Response]
	Unread int64 `json:"unread"`
}

// listCursor is the position in the list: the id of the last notification shown.
type listCursor struct {
	ID int64 `json:"id"`
}

// list answers GET /notifications?unread=true&limit=20&after=…, from the newest to the oldest.
// It is paginated by cursor, like a "load more": the next page is ?after= with meta.next.
func (s *Service) list(w http.ResponseWriter, r *http.Request) error {
	page, err := httpx.ParseCursorPage(r)
	if err != nil {
		return err
	}
	cursor, err := httpx.DecodeCursor[listCursor](page.After)
	if err != nil {
		return err
	}
	var after *int64
	if cursor != nil {
		after = &cursor.ID
	}
	var unreadOnly bool
	switch r.URL.Query().Get("unread") {
	case "", "false":
	case "true":
		unreadOnly = true
	default:
		return httpx.Validation(httpx.Fields{"unread": i18n.M("validation.boolean")})
	}

	user := auth.MustUser(r.Context())
	// One more than the page: if it comes, there is a next page.
	rows, err := s.q.ListNotifications(r.Context(), store.ListNotificationsParams{
		UserID: user.ID, UnreadOnly: unreadOnly, After: after, Limit: page.Limit + 1,
	})
	if err != nil {
		return fmt.Errorf("list notifications: %w", err)
	}
	unread, err := s.q.CountUnreadNotifications(r.Context(), user.ID)
	if err != nil {
		return fmt.Errorf("count unread notifications: %w", err)
	}

	t := i18n.FromContext(r.Context())
	data := make([]Response, len(rows))
	for i, n := range rows {
		data[i] = toResponse(n, t)
	}
	httpx.Write(w, http.StatusOK, listResponse{
		CursorList: httpx.NewCursorList(data, page.Limit, func(n Response) any { return listCursor{ID: n.ID} }),
		Unread:     unread,
	})
	return nil
}

// markRead answers POST /notifications/{id}/read.
func (s *Service) markRead(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.PathID(r, "id")
	if err != nil {
		return err
	}
	user := auth.MustUser(r.Context())

	n, err := s.q.MarkNotificationRead(r.Context(), store.MarkNotificationReadParams{ID: id, UserID: user.ID})
	if errors.Is(err, pgx.ErrNoRows) {
		return errNotFound
	}
	if err != nil {
		return fmt.Errorf("mark notification %d: %w", id, err)
	}

	s.broker.PublishToUser(r.Context(), user.ID, realtime.Event{Type: EventRead, Data: map[string]int64{"id": id}})
	httpx.Write(w, http.StatusOK, toResponse(n, i18n.FromContext(r.Context())))
	return nil
}

// markAllRead answers POST /notifications/read-all.
func (s *Service) markAllRead(w http.ResponseWriter, r *http.Request) error {
	user := auth.MustUser(r.Context())
	if _, err := s.q.MarkAllNotificationsRead(r.Context(), user.ID); err != nil {
		return fmt.Errorf("mark notifications: %w", err)
	}

	s.broker.PublishToUser(r.Context(), user.ID, realtime.Event{Type: EventRead, Data: map[string]bool{"all": true}})
	httpx.Write(w, http.StatusNoContent, nil)
	return nil
}
