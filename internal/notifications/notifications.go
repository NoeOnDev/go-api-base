// Package notifications stores each user's notifications and sends them live through
// GET /events (event "notification"), like Laravel's database and broadcast notifications.
//
// To notify from anywhere: service.Notify(ctx, userID, "kind", data).
// It has ownership like notes: each user sees and marks only their own.
package notifications

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/NoeOnDev/go-api-base/internal/i18n"
	"github.com/NoeOnDev/go-api-base/internal/realtime"
	"github.com/NoeOnDev/go-api-base/internal/store"
)

// Kinds of event sent through GET /events.
const (
	// EventNotification arrives with each new notification (with the whole notification).
	EventNotification = "notification"
	// EventRead arrives when the user marks notifications as read in another tab or device, to
	// update the counter: {"id": 5} or {"all": true}.
	EventRead = "notification.read"
)

// Service creates and sends notifications.
type Service struct {
	q       *store.Queries
	broker  *realtime.Broker
	catalog *i18n.Catalog
}

// NewService creates the service. catalog builds the text of each notification (see
// Response.Message); if nil, i18n.Default() is used.
func NewService(q *store.Queries, broker *realtime.Broker, catalog *i18n.Catalog) *Service {
	if catalog == nil {
		catalog = i18n.Default()
	}
	return &Service{q: q, broker: broker, catalog: catalog}
}

// Response is a notification as the frontend sees it.
type Response struct {
	ID   int64  `json:"id"`
	Kind string `json:"kind"`
	// Message is the text in the language of whoever reads it, built with the "notifications.<kind>"
	// key of the catalogs and the values of Data (see docs/i18n.md). It is not stored: that way each
	// one reads it in their language. Empty if the kind has no text in the catalogs; the frontend
	// builds it from Kind and Data.
	Message   string          `json:"message"`
	Data      json.RawMessage `json:"data"`
	ReadAt    *time.Time      `json:"read_at"`
	CreatedAt time.Time       `json:"created_at"`
}

func toResponse(n store.Notification, t i18n.Translator) Response {
	resp := Response{ID: n.ID, Kind: n.Kind, Data: n.Data, ReadAt: n.ReadAt, CreatedAt: n.CreatedAt}
	if key := "notifications." + n.Kind; t.Has(key) {
		var data map[string]any
		_ = json.Unmarshal(n.Data, &data) // if Data is not an object, the text goes without its values
		args := make([]any, 0, 2*len(data))
		for k, v := range data {
			args = append(args, k, v)
		}
		resp.Message = t.T(key, args...)
	}
	return resp
}

// Notify stores the notification and sends it live to the user's open connections.
// kind is the kind (for example "password_changed"); data, what the frontend needs to show it,
// and it is stored as JSON.
func (s *Service) Notify(ctx context.Context, userID int64, kind string, data any) error {
	encoded, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("encode the notification: %w", err)
	}
	n, err := s.q.CreateNotification(ctx, store.CreateNotificationParams{UserID: userID, Kind: kind, Data: encoded})
	if err != nil {
		return fmt.Errorf("store the notification: %w", err)
	}
	// The live event goes in the user's language, not in that of whoever made the change (a
	// security notice can be triggered by someone else). If it cannot be read, it goes in the
	// default language.
	locale, err := s.q.GetUserLocale(ctx, userID)
	if err != nil || locale == nil {
		locale = new(string)
	}
	s.broker.PublishToUser(ctx, userID, realtime.Event{Type: EventNotification, Data: toResponse(n, s.catalog.Translator(*locale))})
	return nil
}
