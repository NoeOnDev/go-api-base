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

// With several API instances, each one has its broker with its open connections. For what is
// published on one to reach the connections of all, the brokers notify each other with
// PostgreSQL's LISTEN/NOTIFY: each instance applies its own right away (it delivers the event
// or cuts the streams) and sends it through NOTIFY, and the others receive it and apply it to
// their connections.

const (
	// maxNotifyPayload is the most a NOTIFY allows: PostgreSQL accepts less than 8000 bytes. A
	// larger message (for example, the event of a long note) is stored in realtime_messages, and
	// only its ID travels through NOTIFY.
	maxNotifyPayload = 7999
	// listenPing is how often the LISTEN connection is checked when nothing arrives. Without it, a
	// connection that drops without warning (a firewall that discards it) would go unnoticed.
	listenPing = time.Minute
	// shareTimeout is the deadline to send a message to the other instances.
	shareTimeout = 5 * time.Second
	// listenerName identifies the LISTEN connection in pg_stat_activity (application_name).
	listenerName = "realtime-listener"
)

// Kinds of message between instances.
const (
	kindEvent             = "event"
	kindSessionEnded      = "session_ended"
	kindUserSessionsEnded = "user_sessions_ended"
)

// message is what travels through NOTIFY.
type message struct {
	// Origin is the instance that sent it: it already applied it, so it ignores it when it arrives.
	Origin string `json:"origin"`
	Kind   string `json:"kind"`
	// For kindEvent: the channel and the event.
	Channel string       `json:"channel,omitempty"`
	Event   *sharedEvent `json:"event,omitempty"`
	// For kindSessionEnded, the closed session. For kindUserSessionsEnded, the user and the session
	// that stays open (empty: none).
	UserID     int64  `json:"user_id,omitempty"`
	SessionKey string `json:"session_key,omitempty"`
	// StoredID is the ID in realtime_messages of a message that did not fit in the NOTIFY.
	StoredID int64 `json:"stored_id,omitempty"`
}

// sharedEvent is an Event with Data already encoded in JSON. That way it reaches the other
// instances as is, without going through map[string]any (which, for example, turns numbers into
// float64).
type sharedEvent struct {
	ID   string          `json:"id,omitempty"`
	Type string          `json:"type,omitempty"`
	Data json.RawMessage `json:"data"`
}

// Listen connects the broker with those of the other API instances: from then on, what is
// published on one reaches the connections of all, and so do the closed sessions. Without
// Listen, the broker lives only in memory.
//
// It uses a connection of its own, apart from the pool. It returns once it is listening, and
// goes on in the background until ctx is cancelled. If the connection drops, it reconnects on
// its own, and then cuts the open streams: while it was not listening some event could have
// been lost, and on reconnecting the browser asks for the state again.
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

// listen opens the connection that receives the messages of the other instances.
func listen(ctx context.Context, pool *pgxpool.Pool) (*pgx.Conn, error) {
	cfg := pool.Config().ConnConfig.Copy()
	cfg.RuntimeParams["application_name"] = listenerName
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("realtime: connect to listen to the other instances: %w", err)
	}
	if err := store.New(conn).ListenRealtime(ctx); err != nil {
		_ = conn.Close(context.WithoutCancel(ctx))
		return nil, fmt.Errorf("realtime: LISTEN: %w", err)
	}
	return conn, nil
}

// receive applies the messages of the other instances until ctx is cancelled, reconnecting when
// the connection drops.
func (b *Broker) receive(ctx context.Context, pool *pgxpool.Pool, conn *pgx.Conn, logger *slog.Logger) {
	for {
		err := b.wait(ctx, conn, logger)
		_ = conn.Close(context.WithoutCancel(ctx))
		if ctx.Err() != nil {
			return
		}
		logger.Warn("realtime: the connection with the other instances dropped; reconnecting", "error", err)

		if conn = reconnect(ctx, pool, logger); conn == nil {
			return
		}
		b.mu.Lock()
		b.closeSubscriptions()
		b.mu.Unlock()
		logger.Info("realtime: reconnected with the other instances; the streams were cut so they ask for the state")
	}
}

// reconnect opens the LISTEN connection again: the first time right away, and then further and
// further apart, up to every 30 seconds. It returns nil if ctx is cancelled.
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
		logger.Warn("realtime: could not reconnect with the other instances", "error", err)
		delay = min(max(2*delay, time.Second), 30*time.Second)
	}
}

// wait receives and applies messages until the connection fails or ctx is cancelled.
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
			// Nothing arrived for a while: check that the connection is still alive.
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

// apply applies a message received from another instance.
func (b *Broker) apply(ctx context.Context, payload string, logger *slog.Logger) {
	var msg message
	if err := json.Unmarshal([]byte(payload), &msg); err != nil {
		logger.Error("realtime: invalid message from another instance", "error", err)
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
			logger.Error("realtime: could not read a stored message from another instance", "id", msg.StoredID, "error", err)
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
		logger.Error("realtime: unknown message kind", "kind", msg.Kind)
	}
}

// shareEvent sends the event to the other instances. Without Listen, it does nothing.
func (b *Broker) shareEvent(ctx context.Context, channel string, ev Event) {
	if b.queries() == nil {
		return
	}
	data, err := json.Marshal(ev.Data)
	if err != nil {
		httpx.LoggerFrom(ctx).Error("realtime: encode the event for the other instances", "type", ev.Type, "error", err)
		return
	}
	b.share(ctx, message{Kind: kindEvent, Channel: channel, Event: &sharedEvent{ID: ev.ID, Type: ev.Type, Data: data}})
}

// share sends the message to the other instances. Without Listen, it does nothing. If it fails,
// it is only logged: like Publish, it is best effort.
func (b *Broker) share(ctx context.Context, msg message) {
	q := b.queries()
	if q == nil {
		return
	}
	// Without the cancel of the request: the message goes out even if the client is already gone.
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
		httpx.LoggerFrom(ctx).Error("realtime: could not notify the other instances", "kind", msg.Kind, "error", err)
	}
}

// queries returns what sends the messages to the other instances, or nil without Listen.
func (b *Broker) queries() *store.Queries {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.q
}
