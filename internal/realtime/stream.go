// Package realtime envía eventos al navegador en tiempo real con Server-Sent Events (SSE).
//
// Tiene dos piezas (ver docs/realtime.md):
//   - Stream: una respuesta SSE. Sirve para suscribirse a eventos (GET /events, con EventSource)
//     y para transmitir una respuesta larga mientras se genera, como la de un agente de IA
//     (un POST que el frontend lee con fetch).
//   - Broker: reparte eventos por canal (uno por usuario y temas como "project:42") entre las
//     conexiones abiertas de GET /events.
package realtime

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Event es un evento SSE.
type Event struct {
	// ID es opcional. El navegador lo reenvía al reconectarse (Last-Event-ID).
	ID string
	// Type es el nombre del evento (por ejemplo "notification"): en el navegador se escucha con
	// addEventListener(Type, …). Vacío es "message", que llega a onmessage.
	Type string
	// Data se envía codificado en JSON.
	Data any
}

// Stream es una respuesta SSE abierta. Es seguro usarlo desde varias goroutines.
type Stream struct {
	mu sync.Mutex
	w  http.ResponseWriter
	rc *http.ResponseController
}

// Open convierte la respuesta en un stream SSE: envía los encabezados y el status 200.
//
// Quita el WriteTimeout del servidor solo para esta petición: con él, el servidor cortaría el
// stream a los 30 segundos. En su lugar, cada escritura tiene su propio plazo (streamWriteTimeout).
// El stream termina cuando el handler vuelve; para enterarse de que el cliente se fue, usar
// r.Context().
func Open(w http.ResponseWriter, r *http.Request) (*Stream, error) {
	rc := http.NewResponseController(w)
	if err := rc.SetWriteDeadline(time.Time{}); err != nil && !errors.Is(err, http.ErrNotSupported) {
		return nil, fmt.Errorf("quitar el timeout de escritura: %w", err)
	}

	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	// Nginx guarda las respuestas en un buffer antes de enviarlas: así no lo hace con esta.
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	if err := rc.Flush(); err != nil {
		return nil, fmt.Errorf("la respuesta no admite streaming: %w", err)
	}
	return &Stream{w: w, rc: rc}, nil
}

// Send envía un evento y lo hace llegar enseguida (Flush).
func (s *Stream) Send(ev Event) error {
	if strings.ContainsAny(ev.ID, "\r\n") || strings.ContainsAny(ev.Type, "\r\n") {
		return errors.New("realtime: el ID y el tipo del evento no pueden tener saltos de línea")
	}
	data, err := json.Marshal(ev.Data)
	if err != nil {
		return fmt.Errorf("realtime: codificar evento: %w", err)
	}

	var b strings.Builder
	if ev.ID != "" {
		b.WriteString("id: " + ev.ID + "\n")
	}
	if ev.Type != "" {
		b.WriteString("event: " + ev.Type + "\n")
	}
	// Cada línea del dato va con su "data:"; así un salto de línea no corta el evento.
	// (El JSON de encoding/json no los tiene, pero no dependemos de eso.)
	for line := range strings.SplitSeq(string(data), "\n") {
		b.WriteString("data: " + line + "\n")
	}
	b.WriteString("\n")

	return s.write(b.String())
}

// Comment envía un comentario, que el navegador ignora. Sirve de latido: evita que un proxy
// cierre la conexión por inactividad.
func (s *Stream) Comment(text string) error {
	return s.write(": " + strings.ReplaceAll(text, "\n", " ") + "\n\n")
}

// streamWriteTimeout es cuánto puede tardar cada escritura del stream. Open quita el WriteTimeout
// del servidor (que cortaría el stream a los 30 segundos), así que hace falta un plazo propio: si
// un cliente deja de leer sin cerrar la conexión, la escritura se bloquearía en cuanto se llena el
// buffer de TCP, y el handler no vería ni el logout ni el apagado.
var streamWriteTimeout = 10 * time.Second

func (s *Stream) write(chunk string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.rc.SetWriteDeadline(time.Now().Add(streamWriteTimeout)); err != nil && !errors.Is(err, http.ErrNotSupported) {
		return err
	}
	if _, err := s.w.Write([]byte(chunk)); err != nil {
		return err
	}
	return s.rc.Flush()
}
