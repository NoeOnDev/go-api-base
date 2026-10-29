package realtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/NoeOnDev/go-api-base/internal/httpx"
	"github.com/NoeOnDev/go-api-base/internal/store"
)

// Con varias instancias de la API, cada una tiene su broker con sus conexiones abiertas. Para que
// lo publicado en una llegue a las conexiones de todas, los brokers se avisan con LISTEN/NOTIFY de
// PostgreSQL: cada instancia aplica lo suyo en el acto (entrega el evento o corta los streams) y
// lo envía por NOTIFY, y las demás lo reciben y lo aplican a sus conexiones.

const (
	// maxNotifyPayload es lo más que admite un NOTIFY: PostgreSQL acepta menos de 8000 bytes. Un
	// mensaje más grande (por ejemplo, el evento de una nota larga) se guarda en realtime_messages,
	// y por NOTIFY viaja solo su id.
	maxNotifyPayload = 7999
	// listenPing es cada cuánto se revisa la conexión de LISTEN si no llega nada. Sin esto, una
	// conexión que se cae sin aviso (un firewall que la descarta) pasaría desapercibida.
	listenPing = time.Minute
	// shareTimeout es el plazo para enviar un mensaje a las demás instancias.
	shareTimeout = 5 * time.Second
	// listenerName identifica la conexión de LISTEN en pg_stat_activity (application_name).
	listenerName = "realtime-listener"
)

// Tipos de mensaje entre instancias.
const (
	kindEvent             = "event"
	kindSessionEnded      = "session_ended"
	kindUserSessionsEnded = "user_sessions_ended"
)

// message es lo que viaja por NOTIFY.
type message struct {
	// Origin es la instancia que lo envió: ya lo aplicó, así que lo ignora al recibirlo.
	Origin string `json:"origin"`
	Kind   string `json:"kind"`
	// Para kindEvent: el canal y el evento.
	Channel string       `json:"channel,omitempty"`
	Event   *sharedEvent `json:"event,omitempty"`
	// Para kindSessionEnded, la sesión cerrada. Para kindUserSessionsEnded, el usuario y la sesión
	// que sigue abierta (vacía: ninguna).
	UserID     int64  `json:"user_id,omitempty"`
	SessionKey string `json:"session_key,omitempty"`
	// StoredID es el id en realtime_messages de un mensaje que no entraba en el NOTIFY.
	StoredID int64 `json:"stored_id,omitempty"`
}

// sharedEvent es un Event con Data ya codificado en JSON. Así llega tal cual a las demás
// instancias, sin pasar por map[string]any (que, por ejemplo, convierte los números en float64).
type sharedEvent struct {
	ID   string          `json:"id,omitempty"`
	Type string          `json:"type,omitempty"`
	Data json.RawMessage `json:"data"`
}

// Listen conecta el broker con los de las demás instancias de la API: desde ahí, lo que se publica
// en una llega a las conexiones de todas, y lo mismo los cierres de sesión. Sin Listen, el broker
// vive solo en memoria.
//
// Usa una conexión propia, aparte del pool. Vuelve cuando ya escucha, y sigue en segundo plano
// hasta que ctx se cancela. Si la conexión se corta, se reconecta sola, y entonces corta los
// streams abiertos: mientras no escuchaba pudo perderse algún evento, y al reconectarse el
// navegador vuelve a pedir el estado.
func (b *Broker) Listen(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger) error {
	conn, err := listen(ctx, pool)
	if err != nil {
		return err
	}
	b.mu.Lock()
	b.q = store.New(pool)
	b.mu.Unlock()
	go b.receive(ctx, pool, conn, logger)
	return nil
}

// listen abre la conexión que recibe los mensajes de las demás instancias.
func listen(ctx context.Context, pool *pgxpool.Pool) (*pgx.Conn, error) {
	cfg := pool.Config().ConnConfig.Copy()
	cfg.RuntimeParams["application_name"] = listenerName
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("realtime: conectar para escuchar a las demás instancias: %w", err)
	}
	if err := store.New(conn).ListenRealtime(ctx); err != nil {
		_ = conn.Close(context.WithoutCancel(ctx))
		return nil, fmt.Errorf("realtime: LISTEN: %w", err)
	}
	return conn, nil
}

// receive aplica los mensajes de las demás instancias hasta que ctx se cancela, reconectándose
// cuando se corta la conexión.
func (b *Broker) receive(ctx context.Context, pool *pgxpool.Pool, conn *pgx.Conn, logger *slog.Logger) {
	for {
		err := b.wait(ctx, conn, logger)
		_ = conn.Close(context.WithoutCancel(ctx))
		if ctx.Err() != nil {
			return
		}
		logger.Warn("realtime: se cortó la conexión con las demás instancias; reconectando", "error", err)

		if conn = reconnect(ctx, pool, logger); conn == nil {
			return
		}
		b.mu.Lock()
		b.closeSubscriptions()
		b.mu.Unlock()
		logger.Info("realtime: reconectado con las demás instancias; se cortaron los streams para que pidan el estado")
	}
}

// reconnect vuelve a abrir la conexión de LISTEN: la primera vez enseguida, y después cada vez
// más espaciado, hasta cada 30 segundos. Devuelve nil si ctx se cancela.
func reconnect(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger) *pgx.Conn {
	var delay time.Duration
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(delay):
		}
		conn, err := listen(ctx, pool)
		if err == nil {
			return conn
		}
		if ctx.Err() != nil {
			return nil
		}
		logger.Warn("realtime: no se pudo reconectar con las demás instancias", "error", err)
		delay = min(max(2*delay, time.Second), 30*time.Second)
	}
}

// wait recibe y aplica mensajes hasta que la conexión falla o ctx se cancela.
func (b *Broker) wait(ctx context.Context, conn *pgx.Conn, logger *slog.Logger) error {
	for {
		waitCtx, cancel := context.WithTimeout(ctx, listenPing)
		n, err := conn.WaitForNotification(waitCtx)
		cancel()
		switch {
		case err == nil:
			b.apply(ctx, n.Payload, logger)
		case ctx.Err() != nil:
			return ctx.Err()
		case errors.Is(err, context.DeadlineExceeded):
			// No llegó nada en un rato: se revisa que la conexión siga viva.
			pingCtx, cancel := context.WithTimeout(ctx, shareTimeout)
			err := conn.Ping(pingCtx)
			cancel()
			if err != nil {
				return err
			}
		default:
			return err
		}
	}
}

// apply aplica un mensaje recibido de otra instancia.
func (b *Broker) apply(ctx context.Context, payload string, logger *slog.Logger) {
	var msg message
	if err := json.Unmarshal([]byte(payload), &msg); err != nil {
		logger.Error("realtime: mensaje de otra instancia inválido", "error", err)
		return
	}
	if msg.Origin == b.id {
		return
	}
	if msg.StoredID != 0 {
		stored, err := b.queries().GetRealtimeMessage(ctx, msg.StoredID)
		if err == nil {
			err = json.Unmarshal([]byte(stored), &msg)
		}
		if err != nil {
			logger.Error("realtime: no se pudo leer un mensaje guardado de otra instancia", "id", msg.StoredID, "error", err)
			return
		}
	}

	switch msg.Kind {
	case kindEvent:
		if msg.Event != nil {
			b.deliver(msg.Channel, Event{ID: msg.Event.ID, Type: msg.Event.Type, Data: msg.Event.Data})
		}
	case kindSessionEnded:
		b.endSession(msg.SessionKey)
	case kindUserSessionsEnded:
		b.endUserSessions(msg.UserID, msg.SessionKey)
	default:
		logger.Error("realtime: tipo de mensaje desconocido", "kind", msg.Kind)
	}
}

// shareEvent envía el evento a las demás instancias. Sin Listen, no hace nada.
func (b *Broker) shareEvent(ctx context.Context, channel string, ev Event) {
	if b.queries() == nil {
		return
	}
	data, err := json.Marshal(ev.Data)
	if err != nil {
		httpx.LoggerFrom(ctx).Error("realtime: codificar el evento para las demás instancias", "type", ev.Type, "error", err)
		return
	}
	b.share(ctx, message{Kind: kindEvent, Channel: channel, Event: &sharedEvent{ID: ev.ID, Type: ev.Type, Data: data}})
}

// share envía el mensaje a las demás instancias. Sin Listen, no hace nada. Si falla, solo se
// registra: como Publish, es mejor esfuerzo.
func (b *Broker) share(ctx context.Context, msg message) {
	q := b.queries()
	if q == nil {
		return
	}
	// Sin el cancel de la petición: el mensaje sale aunque el cliente ya se haya ido.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shareTimeout)
	defer cancel()

	msg.Origin = b.id
	payload, err := json.Marshal(msg)
	if err == nil {
		if len(payload) <= maxNotifyPayload {
			err = q.NotifyRealtime(ctx, string(payload))
		} else {
			err = q.NotifyStoredRealtimeMessage(ctx, store.NotifyStoredRealtimeMessageParams{Origin: b.id, Payload: string(payload)})
		}
	}
	if err != nil {
		httpx.LoggerFrom(ctx).Error("realtime: no se pudo avisar a las demás instancias", "kind", msg.Kind, "error", err)
	}
}

// queries devuelve con qué enviar los mensajes a las demás instancias, o nil sin Listen.
func (b *Broker) queries() *store.Queries {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.q
}
