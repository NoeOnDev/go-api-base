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

	"github.com/NoeOnDev/go-api-base/internal/i18n"
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
	q       *store.Queries
	broker  *realtime.Broker
	catalog *i18n.Catalog
}

// NewService crea el servicio. catalog arma el texto de cada notificación (ver Response.Message);
// si es nil, se usa i18n.Default().
func NewService(q *store.Queries, broker *realtime.Broker, catalog *i18n.Catalog) *Service {
	if catalog == nil {
		catalog = i18n.Default()
	}
	return &Service{q: q, broker: broker, catalog: catalog}
}

// Response es una notificación como la ve el frontend.
type Response struct {
	ID   int64  `json:"id"`
	Kind string `json:"kind"`
	// Message es el texto en el idioma de quien la lee, armado con la clave "notifications.<kind>"
	// de los catálogos y los valores de Data (ver docs/i18n.md). No se guarda: así cada uno la lee
	// en su idioma. Vacío si el tipo no tiene texto en los catálogos; el frontend la arma con Kind
	// y Data.
	Message   string          `json:"message"`
	Data      json.RawMessage `json:"data"`
	ReadAt    *time.Time      `json:"read_at"`
	CreatedAt time.Time       `json:"created_at"`
}

func toResponse(n store.Notification, t i18n.Translator) Response {
	resp := Response{ID: n.ID, Kind: n.Kind, Data: n.Data, ReadAt: n.ReadAt, CreatedAt: n.CreatedAt}
	if key := "notifications." + n.Kind; t.Has(key) {
		var data map[string]any
		_ = json.Unmarshal(n.Data, &data) // si Data no es un objeto, el texto queda sin sus valores
		args := make([]any, 0, 2*len(data))
		for k, v := range data {
			args = append(args, k, v)
		}
		resp.Message = t.T(key, args...)
	}
	return resp
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
	// El evento en vivo va en el idioma del usuario, no en el de quien hizo el cambio (un aviso de
	// seguridad puede dispararlo otra persona). Si no se puede leer, va en el idioma por defecto.
	locale, err := s.q.GetUserLocale(ctx, userID)
	if err != nil || locale == nil {
		locale = new(string)
	}
	s.broker.PublishToUser(ctx, userID, realtime.Event{Type: EventNotification, Data: toResponse(n, s.catalog.Translator(*locale))})
	return nil
}
