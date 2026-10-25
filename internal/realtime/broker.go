package realtime

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
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
	// ErrForbiddenTopic indica que el tema no existe o que el usuario no puede escucharlo.
	ErrForbiddenTopic = errors.New("realtime: tema no permitido")
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
// Vive en memoria: alcanza para una instancia de la API. Con varias, los eventos se repartirían
// con LISTEN/NOTIFY de PostgreSQL (ver docs/realtime.md).
type Broker struct {
	mu          sync.Mutex
	channels    map[string]map[*subscription]struct{}
	perUser     map[int64]int
	total       int
	authorizers map[string]Authorizer
	done        chan struct{}

	maxPerUser, maxTotal int
}

// subscription es una conexión abierta de GET /events.
type subscription struct {
	userID   int64
	channels []string
	events   chan Event
	closed   chan struct{}
	once     sync.Once
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

// Publish envía el evento a todas las conexiones que escuchan el canal. No bloquea: si una
// conexión no lee a tiempo, se cierra. Es "mejor esfuerzo": la fuente de verdad es la base,
// y el frontend vuelve a pedir el estado al reconectarse.
//
// Recibe ctx para que se pueda cambiar por una implementación que publique en otro lado
// (por ejemplo, PostgreSQL) sin tocar a quien publica.
func (b *Broker) Publish(_ context.Context, channel string, ev Event) {
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
	for _, subs := range b.channels {
		for sub := range subs {
			sub.close()
		}
	}
}

// authorizeTopics comprueba que el usuario pueda escuchar cada tema.
func (b *Broker) authorizeTopics(ctx context.Context, userID int64, topics []string) error {
	for _, topic := range topics {
		kind, id, ok := strings.Cut(topic, ":")
		b.mu.Lock()
		fn := b.authorizers[kind]
		b.mu.Unlock()
		if !ok || id == "" || fn == nil {
			return ErrForbiddenTopic
		}
		allowed, err := fn(ctx, userID, id)
		if err != nil {
			return err
		}
		if !allowed {
			return ErrForbiddenTopic
		}
	}
	return nil
}

// subscribe abre una conexión del usuario, que escucha su canal y los temas indicados
// (ya autorizados).
func (b *Broker) subscribe(userID int64, topics []string) (*subscription, error) {
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
		userID:   userID,
		channels: append([]string{UserChannel(userID)}, topics...),
		events:   make(chan Event, subscriptionBuffer),
		closed:   make(chan struct{}),
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
