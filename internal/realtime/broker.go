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

// Límites por defecto de conexiones abiertas a GET /events. Cada conexión es una goroutine y
// un socket abiertos mientras dure, así que sin tope cualquiera podría agotar los recursos.
const (
	defaultMaxPerUser = 10   // pestañas o dispositivos de un mismo usuario
	defaultMaxTotal   = 1000 // entre todos los usuarios
	// subscriptionBuffer es cuántos eventos se guardan para una conexión que todavía no los leyó.
	// Si se llena, el cliente es demasiado lento: se cierra su conexión y el navegador se reconecta.
	subscriptionBuffer = 64
)

var (
	// ErrTooManyConnections indica que se superó el límite de conexiones (del usuario o total).
	ErrTooManyConnections = errors.New("realtime: demasiadas conexiones abiertas")
	// ErrClosed indica que el broker se cerró (la API se está apagando).
	ErrClosed = errors.New("realtime: el servidor se está apagando")
)

// Authorizer decide si un usuario puede escuchar un tema. Recibe el id del tema: para
// "project:42", el id es "42".
type Authorizer func(ctx context.Context, userID int64, id string) (bool, error)

// Broker reparte eventos entre las conexiones abiertas de GET /events.
//
// Hay dos tipos de canal:
//   - El del usuario ("user:<id>"): cada conexión escucha el suyo, sin pedirlo. Para
//     notificaciones y cambios en los datos propios.
//   - Los temas ("project:42"): para que varios usuarios vean en vivo lo mismo. Cada tipo de
//     tema necesita un Authorizer (ver Authorize); un tema sin autorizador se rechaza.
//
// Cada instancia de la API tiene su broker, con sus conexiones. Con Listen, los brokers de todas
// las instancias se avisan por PostgreSQL: un evento publicado en cualquiera llega a las
// conexiones de todas (ver instances.go). Sin Listen vive solo en memoria, como en los tests.
type Broker struct {
	mu          sync.Mutex
	channels    map[string]map[*subscription]struct{}
	perUser     map[int64]int
	total       int
	authorizers map[string]Authorizer
	done        chan struct{}

	maxPerUser, maxTotal int

	// id identifica a esta instancia en los mensajes entre instancias, y q los envía. q es nil
	// mientras no se llame a Listen.
	id string
	q  *store.Queries
}

// subscription es una conexión abierta de GET /events.
type subscription struct {
	userID     int64
	sessionKey string // la sesión con la que se abrió (ver auth.Service.SessionKey)
	channels   []string
	events     chan Event
	closed     chan struct{}
	once       sync.Once
}

func (s *subscription) close() { s.once.Do(func() { close(s.closed) }) }

// NewBroker crea un broker con los límites por defecto.
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

// UserChannel es el canal de un usuario.
func UserChannel(userID int64) string { return "user:" + strconv.FormatInt(userID, 10) }

// Authorize registra quién puede escuchar los temas de un tipo, por ejemplo "project".
// Se llama al registrar las rutas del módulo dueño de ese tipo de dato.
func (b *Broker) Authorize(kind string, fn Authorizer) {
	if kind == "user" || kind == "" || strings.Contains(kind, ":") {
		panic("realtime: tipo de tema inválido: " + kind)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.authorizers[kind] = fn
}

// Publish envía el evento a todas las conexiones que escuchan el canal, en esta instancia y (con
// Listen) en las demás. No bloquea: si una conexión no lee a tiempo, se cierra. Es "mejor
// esfuerzo": la fuente de verdad es la base, y el frontend vuelve a pedir el estado al reconectarse.
func (b *Broker) Publish(ctx context.Context, channel string, ev Event) {
	b.deliver(channel, ev)
	b.shareEvent(ctx, channel, ev)
}

// deliver entrega el evento a las conexiones de esta instancia.
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

// PublishToUser envía el evento a todas las conexiones del usuario.
func (b *Broker) PublishToUser(ctx context.Context, userID int64, ev Event) {
	b.Publish(ctx, UserChannel(userID), ev)
}

// SessionEnded corta los streams abiertos con esa sesión, en todas las instancias. Auth lo llama
// en el logout (el broker implementa auth.SessionWatcher).
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

// UserSessionsEnded corta los streams del usuario abiertos con otras sesiones que no sean
// keepSessionKey (vacío: todas), en todas las instancias. Auth lo llama cuando cierra sesiones del
// usuario: al cambiar o recuperar la contraseña y al activar o desactivar el 2FA. Así, quien tenía
// una sesión robada deja de recibir eventos en el acto, antes del aviso de "se cambió tu contraseña".
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

// DisconnectUser corta todos los streams del usuario, en todas las instancias. Sirve para quitarle
// el acceso a un tema al instante (por ejemplo, al sacarlo de un proyecto): el navegador se
// reconecta solo, y en la reconexión se vuelven a revisar sus permisos.
func (b *Broker) DisconnectUser(ctx context.Context, userID int64) {
	b.UserSessionsEnded(ctx, userID, "")
}

// Done se cierra cuando el broker se cierra (al apagar la API). Un stream largo que no use el
// broker, como la respuesta de un agente de IA, puede escucharlo para terminar a tiempo.
func (b *Broker) Done() <-chan struct{} { return b.done }

// Close cierra todas las conexiones. Se llama al apagar la API: sin esto, el servidor esperaría
// hasta el timeout, porque un stream SSE nunca termina solo.
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

// closeSubscriptions cierra todas las conexiones de esta instancia. Se llama con b.mu tomado.
func (b *Broker) closeSubscriptions() {
	for _, subs := range b.channels {
		for sub := range subs {
			sub.close()
		}
	}
}

// authorizeTopics separa los temas que el usuario puede escuchar de los rechazados: los que no
// tienen autorizador, están mal escritos o el autorizador no permite.
func (b *Broker) authorizeTopics(ctx context.Context, userID int64, topics []string) (allowed, rejected []string, err error) {
	allowed, rejected = []string{}, []string{} // vacías, no nil: el evento ready envía [] y no null
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

// subscribe abre una conexión del usuario con la sesión indicada, que escucha su canal y los
// temas indicados (ya autorizados).
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

// unsubscribe cierra la conexión y la saca de sus canales.
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
