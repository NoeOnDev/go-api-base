package realtime

import (
	"errors"
	"math/rand/v2"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/NoeOnDev/go-api-base/internal/auth"
	"github.com/NoeOnDev/go-api-base/internal/httpx"
)

// heartbeatInterval es cada cuánto se envía un latido y se revisa que la sesión siga vigente.
// Los proxies suelen cortar una conexión sin tráfico al minuto (Nginx, por defecto); 25 segundos
// queda bien por debajo.
var heartbeatInterval = 25 * time.Second

// maxStreamLifetime es cuánto dura, en promedio, un stream. Al cortarse, el navegador se reconecta
// solo, y en la reconexión se vuelven a revisar la sesión y los permisos de cada tema: así, a
// quien le quitaron el acceso a un tema deja de escucharlo aunque nadie lo desconecte.
var maxStreamLifetime = 15 * time.Minute

// streamLifetime devuelve la vida de un stream: maxStreamLifetime ± 2/15 al azar (entre 13 y 17
// minutos). Después de un reinicio todas las pestañas se conectan juntas; con la variación, sus
// renovaciones se reparten en lugar de llegar todas a la vez a pedir su estado.
func streamLifetime() time.Duration {
	spread := maxStreamLifetime * 2 / 15
	return maxStreamLifetime - spread + rand.N(2*spread+1)
}

// Sessions es lo que el stream necesita de auth (lo implementa auth.Service): saber con qué
// sesión se abrió, y revisar con cada latido que siga vigente (no se cerró ni venció).
type Sessions interface {
	SessionKey(r *http.Request) string
	CheckSession(r *http.Request) error
}

// maxTopics es cuántos temas puede escuchar una conexión.
const maxTopics = 20

// Handler responde GET /events: abre un stream SSE con los eventos del usuario y de los temas
// de ?topics=project:42,board:7 que tenga permiso para escuchar. Va detrás de RequireUser.
//
// El primer evento es "ready": {"topics": [los que escucha], "rejected": [los que no tiene
// permiso de escuchar]}. Un tema rechazado no corta la conexión: el usuario sigue recibiendo su
// canal, y el frontend sabe qué tema perdió (por ejemplo, porque lo sacaron de un proyecto).
// Al reconectarse, el frontend tiene que volver a pedir el estado por REST: los eventos de
// mientras no se reenvían.
//
// El stream se corta cuando se cierra su sesión: en el acto si auth avisa (SessionEnded,
// UserSessionsEnded), y si no, en el próximo latido. Y dura como mucho maxStreamLifetime.
func (b *Broker) Handler(sessions Sessions) http.Handler {
	return httpx.Handle(func(w http.ResponseWriter, r *http.Request) error {
		user := auth.MustUser(r.Context())

		topics, err := parseTopics(r.URL.Query().Get("topics"))
		if err != nil {
			return err
		}
		allowed, rejected, err := b.authorizeTopics(r.Context(), user.ID, topics)
		if err != nil {
			return err
		}

		sub, err := b.subscribe(user.ID, sessions.SessionKey(r), allowed)
		switch {
		case errors.Is(err, ErrTooManyConnections):
			w.Header().Set("Retry-After", "30")
			return &httpx.Error{Status: http.StatusTooManyRequests, Code: "too_many_connections"}
		case errors.Is(err, ErrClosed):
			return &httpx.Error{Status: http.StatusServiceUnavailable, Code: "shutting_down"}
		case err != nil:
			return err
		}
		defer b.unsubscribe(sub)

		stream, err := Open(w, r)
		if err != nil {
			return err
		}
		if err := stream.Send(Event{Type: "ready", Data: readyData{Topics: allowed, Rejected: rejected}}); err != nil {
			return nil // el cliente ya se fue
		}

		heartbeat := time.NewTicker(heartbeatInterval)
		defer heartbeat.Stop()
		lifetime := time.NewTimer(streamLifetime())
		defer lifetime.Stop()
		for {
			select {
			case <-r.Context().Done():
				return nil // el cliente cerró la conexión
			case <-sub.closed:
				return nil // sesión cerrada, cliente lento o la API se apaga
			case <-lifetime.C:
				return nil // el navegador se reconecta, y ahí se revisa todo de nuevo
			case ev := <-sub.events:
				if err := stream.Send(ev); err != nil {
					return nil
				}
			case <-heartbeat.C:
				// Una sesión cerrada o vencida corta el stream. Al reconectarse recibe 401, y el
				// EventSource del navegador deja de intentar (el frontend lo ve en onerror).
				if err := sessions.CheckSession(r); err != nil {
					return nil
				}
				if err := stream.Comment("ping"); err != nil {
					return nil
				}
			}
		}
	})
}

// readyData es el contenido del evento ready.
type readyData struct {
	Topics   []string `json:"topics"`
	Rejected []string `json:"rejected"`
}

// parseTopics lee la lista de temas, sin repetidos.
func parseTopics(raw string) ([]string, error) {
	var topics []string
	for t := range strings.SplitSeq(raw, ",") {
		t = strings.TrimSpace(t)
		if t != "" && !slices.Contains(topics, t) {
			topics = append(topics, t)
		}
	}
	if len(topics) > maxTopics {
		return nil, httpx.BadRequest("too_many_topics", "max", maxTopics)
	}
	return topics, nil
}
