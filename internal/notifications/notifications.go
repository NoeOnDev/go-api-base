// Package notifications guarda las notificaciones de cada usuario y se las envía en vivo
// por GET /events (evento "notification"), como las notificaciones de base de datos y
// broadcast de Laravel.
//
// Para notificar desde cualquier parte: service.Notify(ctx, userID, "kind", datos).
// Tiene pertenencia como notes: cada usuario ve y marca solo las suyas.
package notifications

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/NoeOnDev/go-api-base/internal/realtime"
	"github.com/NoeOnDev/go-api-base/internal/store"
)

// Tipos de evento que se envían por GET /events.
const (
	// EventNotification llega con cada notificación nueva (con la notificación completa).
	EventNotification = "notification"
	// EventRead llega cuando el usuario marca notificaciones como leídas en otra pestaña o
	// dispositivo, para actualizar el contador: {"id": 5} o {"all": true}.
	EventRead = "notification.read"
)

// Service crea y envía notificaciones.
type Service struct {
	q      *store.Queries
	broker *realtime.Broker
}

// NewService crea el servicio.
func NewService(q *store.Queries, broker *realtime.Broker) *Service {
	return &Service{q: q, broker: broker}
}

// Response es una notificación como la ve el frontend.
type Response struct {
	ID        int64           `json:"id"`
	Kind      string          `json:"kind"`
	Data      json.RawMessage `json:"data"`
	ReadAt    *time.Time      `json:"read_at"`
	CreatedAt time.Time       `json:"created_at"`
}

func toResponse(n store.Notification) Response {
	return Response{ID: n.ID, Kind: n.Kind, Data: n.Data, ReadAt: n.ReadAt, CreatedAt: n.CreatedAt}
}

// Notify guarda la notificación y la envía en vivo a las conexiones abiertas del usuario.
// kind es el tipo (por ejemplo "password_changed"); data, lo que el frontend necesita para
// mostrarla, y se guarda como JSON.
func (s *Service) Notify(ctx context.Context, userID int64, kind string, data any) error {
	encoded, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("codificar notificación: %w", err)
	}
	n, err := s.q.CreateNotification(ctx, store.CreateNotificationParams{UserID: userID, Kind: kind, Data: encoded})
	if err != nil {
		return fmt.Errorf("guardar notificación: %w", err)
	}
	s.broker.PublishToUser(ctx, userID, realtime.Event{Type: EventNotification, Data: toResponse(n)})
	return nil
}
