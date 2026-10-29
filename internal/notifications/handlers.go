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

// listResponse es el listado paginado, con la cantidad sin leer para el contador del frontend.
type listResponse struct {
	httpx.List[Response]
	Unread int64 `json:"unread"`
}

// list responde GET /notifications?unread=true&limit=20&offset=0, de la más nueva a la más vieja.
func (s *Service) list(w http.ResponseWriter, r *http.Request) error {
	page, err := httpx.ParsePage(r)
	if err != nil {
		return err
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
	rows, err := s.q.ListNotifications(r.Context(), store.ListNotificationsParams{
		UserID: user.ID, UnreadOnly: unreadOnly, Limit: page.Limit, Offset: page.Offset,
	})
	if err != nil {
		return fmt.Errorf("listar notificaciones: %w", err)
	}
	total, err := s.q.CountNotifications(r.Context(), store.CountNotificationsParams{UserID: user.ID, UnreadOnly: unreadOnly})
	if err != nil {
		return fmt.Errorf("contar notificaciones: %w", err)
	}
	unread, err := s.q.CountUnreadNotifications(r.Context(), user.ID)
	if err != nil {
		return fmt.Errorf("contar notificaciones sin leer: %w", err)
	}

	t := i18n.FromContext(r.Context())
	data := make([]Response, len(rows))
	for i, n := range rows {
		data[i] = toResponse(n, t)
	}
	httpx.Write(w, http.StatusOK, listResponse{
		List:   httpx.List[Response]{Data: data, Meta: httpx.PageMeta{Limit: page.Limit, Offset: page.Offset, Total: total}},
		Unread: unread,
	})
	return nil
}

// markRead responde POST /notifications/{id}/read.
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
		return fmt.Errorf("marcar notificación %d: %w", id, err)
	}

	s.broker.PublishToUser(r.Context(), user.ID, realtime.Event{Type: EventRead, Data: map[string]int64{"id": id}})
	httpx.Write(w, http.StatusOK, toResponse(n, i18n.FromContext(r.Context())))
	return nil
}

// markAllRead responde POST /notifications/read-all.
func (s *Service) markAllRead(w http.ResponseWriter, r *http.Request) error {
	user := auth.MustUser(r.Context())
	if _, err := s.q.MarkAllNotificationsRead(r.Context(), user.ID); err != nil {
		return fmt.Errorf("marcar notificaciones: %w", err)
	}

	s.broker.PublishToUser(r.Context(), user.ID, realtime.Event{Type: EventRead, Data: map[string]bool{"all": true}})
	httpx.Write(w, http.StatusNoContent, nil)
	return nil
}
