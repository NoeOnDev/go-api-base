package realtime

import (
	"context"
	"crypto/rand"
	"errors"
	"strconv"
	"strings"
	"sync"

	"github.com/NoeOnDev/go-api-base/internal/store"
)

// Default limits of open GET /events connections. Each connection is a goroutine and a socket
// open for as long as it lasts, so without a cap anyone could use up the resources.
const (
	defaultMaxPerUser = 10   // tabs or devices of the same user
	defaultMaxTotal   = 1000 // across all users
	// subscriptionBuffer is how many events are kept for a connection that has not read them yet.
	// If it fills up, the client is too slow: its connection is closed and the browser reconnects.
	subscriptionBuffer = 64
)

var (
	// ErrTooManyConnections means the connection limit (per user or in total) was exceeded.
	ErrTooManyConnections = errors.New("realtime: demasiadas conexiones abiertas")
	// ErrClosed means the broker was closed (the API is shutting down).
	ErrClosed = errors.New("realtime: the server is shutting down")
)

// Authorizer decides whether a user can listen to a topic. It receives the topic ID: for
// "project:42", the ID is "42".
type Authorizer func(ctx context.Context, userID int64, id string) (bool, error)

// Broker hands out events among the open GET /events connections.
//
// There are two kinds of channel:
//   - The user's ("user:<id>"): each connection listens to its own, without asking. For
//     notifications and changes to the user's own data.
//   - Topics ("project:42"): for several users to see the same thing live. Each kind of topic
//     needs an Authorizer (see Authorize); a topic without an authorizer is rejected.
//
// Each API instance has its broker, with its connections. With Listen, the brokers of every
// instance notify each other through PostgreSQL: an event published on any of them reaches the
// connections of all (see instances.go). Without Listen it lives only in memory, as in the tests.
type Broker struct {
	mu          sync.Mutex
	channels    map[string]map[*subscription]struct{}
	perUser     map[int64]int
	total       int
	authorizers map[string]Authorizer
	done        chan struct{}

	maxPerUser, maxTotal int

	// id identifies this instance in the messages between instances, and q sends them. q is nil
	// until Listen is called.
	id string
	q  *store.Queries
}

// subscription is an open GET /events connection.
type subscription struct {
	userID     int64
	sessionKey string // the session it was opened with (see auth.Service.SessionKey)
	channels   []string
	events     chan Event
	closed     chan struct{}
	once       sync.Once
}

func (s *subscription) close() { s.once.Do(func() { close(s.closed) }) }

// NewBroker creates a broker with the default limits.
func NewBroker() *Broker {
	return &Broker{
		channels:    map[string]map[*subscription]struct{}{},
		perUser:     map[int64]int{},
		authorizers: map[string]Authorizer{},
		done:        make(chan struct{}),
		maxPerUser:  defaultMaxPerUser,
		maxTotal:    defaultMaxTotal,
		id:          rand.Text(),
	}
}

// UserChannel is the channel of a user.
func UserChannel(userID int64) string { return "user:" + strconv.FormatInt(userID, 10) }

// Authorize registers who can listen to the topics of a kind, for example "project".
// It is called when registering the routes of the module that owns that kind of data.
func (b *Broker) Authorize(kind string, fn Authorizer) {
	if kind == "user" || kind == "" || strings.Contains(kind, ":") {
		panic("realtime: invalid topic kind: " + kind)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.authorizers[kind] = fn
}

// Publish sends the event to every connection listening to the channel, on this instance and
// (with Listen) on the others. It does not block: if a connection does not read in time, it is
// closed. It is "best effort": the source of truth is the database, and the frontend asks for
// the state again when it reconnects.
func (b *Broker) Publish(ctx context.Context, channel string, ev Event) {
	b.deliver(channel, ev)
	b.shareEvent(ctx, channel, ev)
}

// deliver hands the event to the connections of this instance.
func (b *Broker) deliver(channel string, ev Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for sub := range b.channels[channel] {
		select {
		case sub.events <- ev:
		default:
			sub.close() // cliente demasiado lento
		}
	}
}

// PublishToUser sends the event to every connection of the user.
func (b *Broker) PublishToUser(ctx context.Context, userID int64, ev Event) {
	b.Publish(ctx, UserChannel(userID), ev)
}

// SessionEnded cuts the streams opened with that session, on every instance. Auth calls it on
// logout (the broker implements auth.SessionWatcher).
func (b *Broker) SessionEnded(ctx context.Context, sessionKey string) {
	b.endSession(sessionKey)
	b.share(ctx, message{Kind: kindSessionEnded, SessionKey: sessionKey})
}

func (b *Broker) endSession(sessionKey string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, subs := range b.channels {
		for sub := range subs {
			if sub.sessionKey == sessionKey {
				sub.close()
			}
		}
	}
}

// UserSessionsEnded cuts the user's streams opened with sessions other than keepSessionKey
// (empty: all of them), on every instance. Auth calls it when it closes the user's sessions: on
// a password change or reset and when two-factor authentication is turned on or off. That way,
// whoever had a stolen session stops getting events right away, before the "your password was
// changed" notice.
func (b *Broker) UserSessionsEnded(ctx context.Context, userID int64, keepSessionKey string) {
	b.endUserSessions(userID, keepSessionKey)
	b.share(ctx, message{Kind: kindUserSessionsEnded, UserID: userID, SessionKey: keepSessionKey})
}

func (b *Broker) endUserSessions(userID int64, keepSessionKey string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for sub := range b.channels[UserChannel(userID)] {
		if keepSessionKey == "" || sub.sessionKey != keepSessionKey {
			sub.close()
		}
	}
}

// DisconnectUser cuts every stream of the user, on every instance. It removes access to a topic
// right away (for example, when the user is taken out of a project): the browser reconnects on
// its own, and on reconnecting their permissions are checked again.
func (b *Broker) DisconnectUser(ctx context.Context, userID int64) {
	b.UserSessionsEnded(ctx, userID, "")
}

// Done is closed when the broker closes (when the API shuts down). A long stream that does not
// use the broker, like the response of an AI agent, can listen to it to end in time.
func (b *Broker) Done() <-chan struct{} { return b.done }

// Close closes every connection. It is called when the API shuts down: without it, the server
// would wait until the timeout, because an SSE stream never ends on its own.
func (b *Broker) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	select {
	case <-b.done:
		return
	default:
	}
	close(b.done)
	b.closeSubscriptions()
}

// closeSubscriptions closes every connection of this instance. It is called with b.mu held.
func (b *Broker) closeSubscriptions() {
	for _, subs := range b.channels {
		for sub := range subs {
			sub.close()
		}
	}
}

// authorizeTopics splits the topics the user can listen to from the rejected ones: those
// without an authorizer, misspelled, or not allowed by the authorizer.
func (b *Broker) authorizeTopics(ctx context.Context, userID int64, topics []string) (allowed, rejected []string, err error) {
	allowed, rejected = []string{}, []string{} // empty, not nil: the ready event sends [] and not null
	for _, topic := range topics {
		kind, id, ok := strings.Cut(topic, ":")
		b.mu.Lock()
		fn := b.authorizers[kind]
		b.mu.Unlock()
		if !ok || id == "" || fn == nil {
			rejected = append(rejected, topic)
			continue
		}
		permitted, err := fn(ctx, userID, id)
		if err != nil {
			return nil, nil, err
		}
		if permitted {
			allowed = append(allowed, topic)
		} else {
			rejected = append(rejected, topic)
		}
	}
	return allowed, rejected, nil
}

// subscribe opens a connection of the user with the given session, which listens to their
// channel and the given topics (already authorized).
func (b *Broker) subscribe(userID int64, sessionKey string, topics []string) (*subscription, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	select {
	case <-b.done:
		return nil, ErrClosed
	default:
	}
	if b.total >= b.maxTotal || b.perUser[userID] >= b.maxPerUser {
		return nil, ErrTooManyConnections
	}

	sub := &subscription{
		userID:     userID,
		sessionKey: sessionKey,
		channels:   append([]string{UserChannel(userID)}, topics...),
		events:     make(chan Event, subscriptionBuffer),
		closed:     make(chan struct{}),
	}
	for _, ch := range sub.channels {
		if b.channels[ch] == nil {
			b.channels[ch] = map[*subscription]struct{}{}
		}
		b.channels[ch][sub] = struct{}{}
	}
	b.perUser[userID]++
	b.total++
	return sub, nil
}

// unsubscribe closes the connection and takes it out of its channels.
func (b *Broker) unsubscribe(sub *subscription) {
	b.mu.Lock()
	defer b.mu.Unlock()
	sub.close()
	for _, ch := range sub.channels {
		delete(b.channels[ch], sub)
		if len(b.channels[ch]) == 0 {
			delete(b.channels, ch)
		}
	}
	b.perUser[sub.userID]--
	if b.perUser[sub.userID] == 0 {
		delete(b.perUser, sub.userID)
	}
	b.total--
}
