package realtime

import (
	"errors"
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

// maxStreamLifetime es cuánto dura como mucho un stream. Al cortarse, el navegador se reconecta
// solo, y en la reconexión se vuelven a revisar la sesión y los permisos de cada tema: así, a
// quien le quitaron el acceso a un tema deja de escucharlo aunque nadie lo desconecte.
var maxStreamLifetime = 15 * time.Minute

// Sessions es lo que el stream necesita de auth (lo implementa auth.Service): saber con qué
// sesión se abrió, y revisar con cada latido que siga vigente (no se cerró ni venció).
type Sessions interface {
	SessionKey(r *http.Request) string
	CheckSession(r *http.Request) error
}

// maxTopics es cuántos temas puede escuchar una conexión.
const maxTopics = 20

// Handler responde GET /events: abre un stream SSE con los eventos del usuario y de los temas
// de ?topics=project:42,board:7 (que tienen que estar autorizados). Va detrás de RequireUser.
//
// El primer evento es "ready", con los temas que se escuchan. Al reconectarse, el frontend
// tiene que volver a pedir el estado por REST: los eventos de mientras no se reenvían.
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
		if err := b.authorizeTopics(r.Context(), user.ID, topics); err != nil {
			if errors.Is(err, ErrForbiddenTopic) {
				return httpx.Forbidden("no podés escuchar alguno de los temas pedidos")
			}
			return err
		}

		sub, err := b.subscribe(user.ID, sessions.SessionKey(r), topics)
		switch {
		case errors.Is(err, ErrTooManyConnections):
			w.Header().Set("Retry-After", "30")
			return &httpx.Error{Status: http.StatusTooManyRequests, Code: "too_many_connections",
				Message: "hay demasiadas conexiones abiertas; cerrá alguna pestaña"}
		case errors.Is(err, ErrClosed):
			return &httpx.Error{Status: http.StatusServiceUnavailable, Code: "shutting_down",
				Message: "el servidor se está reiniciando; volvé a conectarte en unos segundos"}
		case err != nil:
			return err
		}
		defer b.unsubscribe(sub)

		stream, err := Open(w, r)
		if err != nil {
			return err
		}
		if err := stream.Send(Event{Type: "ready", Data: map[string][]string{"topics": topics}}); err != nil {
			return nil // el cliente ya se fue
		}

		heartbeat := time.NewTicker(heartbeatInterval)
		defer heartbeat.Stop()
		lifetime := time.NewTimer(maxStreamLifetime)
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

// parseTopics lee la lista de temas, sin repetidos.
func parseTopics(raw string) ([]string, error) {
	topics := []string{} // vacía, no nil: el evento ready envía [] y no null
	for t := range strings.SplitSeq(raw, ",") {
		t = strings.TrimSpace(t)
		if t != "" && !slices.Contains(topics, t) {
			topics = append(topics, t)
		}
	}
	if len(topics) > maxTopics {
		return nil, httpx.BadRequest("se pueden escuchar como mucho 20 temas por conexión")
	}
	return topics, nil
}
