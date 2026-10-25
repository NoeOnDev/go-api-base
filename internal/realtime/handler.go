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

// heartbeatInterval es cada cuánto se envía un latido. Los proxies suelen cortar una conexión
// sin tráfico al minuto (Nginx, por defecto); 25 segundos queda bien por debajo.
var heartbeatInterval = 25 * time.Second

// maxTopics es cuántos temas puede escuchar una conexión.
const maxTopics = 20

// Handler responde GET /events: abre un stream SSE con los eventos del usuario y de los temas
// de ?topics=project:42,board:7 (que tienen que estar autorizados). Va detrás de RequireUser.
//
// El primer evento es "ready", con los temas que se escuchan. Al reconectarse, el frontend
// tiene que volver a pedir el estado por REST: los eventos de mientras no se reenvían.
func (b *Broker) Handler() http.Handler {
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

		sub, err := b.subscribe(user.ID, topics)
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
		for {
			select {
			case <-r.Context().Done():
				return nil // el cliente cerró la conexión
			case <-sub.closed:
				return nil // cliente lento, o la API se apaga: el navegador se reconecta solo
			case ev := <-sub.events:
				if err := stream.Send(ev); err != nil {
					return nil
				}
			case <-heartbeat.C:
				if err := stream.Comment("ping"); err != nil {
					return nil
				}
			}
		}
	})
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
		return nil, httpx.BadRequest("se pueden escuchar como mucho 20 temas por conexión")
	}
	return topics, nil
}
