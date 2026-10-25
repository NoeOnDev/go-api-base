// Package ssetest lee streams SSE en los tests, como lo haría EventSource en el navegador.
package ssetest

import (
	"bufio"
	"io"
	"strings"
	"testing"
	"time"
)

// Event es un evento leído del stream. Comment tiene el texto de un comentario (": ping").
type Event struct {
	ID, Type, Data, Comment string
}

// Reader lee eventos de un stream SSE en otra goroutine.
type Reader struct {
	events chan Event
	done   chan struct{}
}

// NewReader empieza a leer el cuerpo de una respuesta SSE.
func NewReader(body io.Reader) *Reader {
	r := &Reader{events: make(chan Event, 64), done: make(chan struct{})}
	go func() {
		defer close(r.done)
		sc := bufio.NewScanner(body)
		var ev Event
		var data []string
		for sc.Scan() {
			line := sc.Text()
			switch {
			case line == "":
				if ev.Comment != "" || len(data) > 0 || ev.Type != "" {
					ev.Data = strings.Join(data, "\n")
					r.events <- ev
				}
				ev, data = Event{}, nil
			case strings.HasPrefix(line, ":"):
				ev.Comment = strings.TrimSpace(line[1:])
			case strings.HasPrefix(line, "id: "):
				ev.ID = line[4:]
			case strings.HasPrefix(line, "event: "):
				ev.Type = line[7:]
			case strings.HasPrefix(line, "data: "):
				data = append(data, line[6:])
			}
		}
	}()
	return r
}

// Next devuelve el próximo evento, o falla el test si no llega antes del timeout.
// Saltea los comentarios salvo que se pida con NextComment.
func (r *Reader) Next(t *testing.T, timeout time.Duration) Event {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case ev := <-r.events:
			if ev.Comment != "" {
				continue
			}
			return ev
		case <-r.done:
			t.Fatal("el stream se cerró antes de recibir el evento")
		case <-deadline:
			t.Fatal("no llegó ningún evento a tiempo")
		}
	}
}

// NextComment devuelve el próximo comentario (por ejemplo, el latido).
func (r *Reader) NextComment(t *testing.T, timeout time.Duration) string {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case ev := <-r.events:
			if ev.Comment != "" {
				return ev.Comment
			}
		case <-r.done:
			t.Fatal("el stream se cerró antes de recibir el comentario")
		case <-deadline:
			t.Fatal("no llegó ningún comentario a tiempo")
		}
	}
}

// Nothing comprueba que no llegue ningún evento durante el tiempo indicado.
func (r *Reader) Nothing(t *testing.T, wait time.Duration) {
	t.Helper()
	deadline := time.After(wait)
	for {
		select {
		case ev := <-r.events:
			if ev.Comment == "" {
				t.Fatalf("llegó un evento que no correspondía: %+v", ev)
			}
		case <-deadline:
			return
		}
	}
}

// Closed indica si el stream terminó antes del timeout.
func (r *Reader) Closed(timeout time.Duration) bool {
	select {
	case <-r.done:
		return true
	case <-time.After(timeout):
		return false
	}
}
