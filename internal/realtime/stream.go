// Package realtime sends events to the browser in real time with Server-Sent Events (SSE).
//
// It has two pieces (see docs/realtime.md):
//   - Stream: an SSE response. It is used to subscribe to events (GET /events, with EventSource)
//     and to stream a long response while it is generated, like that of an AI agent (a POST the
//     frontend reads with fetch).
//   - Broker: it hands out events per channel (one per user and topics like "project:42") among
//     the open GET /events connections.
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

// Event is an SSE event.
type Event struct {
	// ID is optional. The browser sends it back when it reconnects (Last-Event-ID).
	ID string
	// Type is the name of the event (for example "notification"): in the browser it is listened to
	// with addEventListener(Type, …). Empty is "message", which reaches onmessage.
	Type string
	// Data is sent encoded as JSON.
	Data any
}

// Stream is an open SSE response. It is safe to use from several goroutines.
type Stream struct {
	mu sync.Mutex
	w  http.ResponseWriter
	rc *http.ResponseController
}

// Open turns the response into an SSE stream: it sends the headers and the 200 status.
//
// It removes the server's WriteTimeout only for this request: with it, the server would cut the
// stream after 30 seconds. Instead, each write has its own deadline (streamWriteTimeout).
// The stream ends when the handler returns; to learn that the client left, use r.Context().
func Open(w http.ResponseWriter, r *http.Request) (*Stream, error) {
	rc := http.NewResponseController(w)
	if err := rc.SetWriteDeadline(time.Time{}); err != nil && !errors.Is(err, http.ErrNotSupported) {
		return nil, fmt.Errorf("remove the write timeout: %w", err)
	}

	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	// Nginx buffers responses before sending them: this way it does not with this one.
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	if err := rc.Flush(); err != nil {
		return nil, fmt.Errorf("the response does not support streaming: %w", err)
	}
	return &Stream{w: w, rc: rc}, nil
}

// Send sends an event and flushes it right away.
func (s *Stream) Send(ev Event) error {
	if strings.ContainsAny(ev.ID, "\r\n") || strings.ContainsAny(ev.Type, "\r\n") {
		return errors.New("realtime: the event ID and type cannot have line breaks")
	}
	data, err := json.Marshal(ev.Data)
	if err != nil {
		return fmt.Errorf("realtime: encode event: %w", err)
	}

	var b strings.Builder
	if ev.ID != "" {
		b.WriteString("id: " + ev.ID + "\n")
	}
	if ev.Type != "" {
		b.WriteString("event: " + ev.Type + "\n")
	}
	// Each line of the data goes with its "data:"; that way a line break does not cut the event.
	// (The JSON of encoding/json has none, but we do not rely on that.)
	for line := range strings.SplitSeq(string(data), "\n") {
		b.WriteString("data: " + line + "\n")
	}
	b.WriteString("\n")

	return s.write(b.String())
}

// Comment sends a comment, which the browser ignores. It works as a heartbeat: it keeps a proxy
// from closing the connection for inactivity.
func (s *Stream) Comment(text string) error {
	return s.write(": " + strings.ReplaceAll(text, "\n", " ") + "\n\n")
}

// streamWriteTimeout is how long each write of the stream can take. Open removes the server's
// WriteTimeout (which would cut the stream after 30 seconds), so a deadline of its own is
// needed: if a client stops reading without closing the connection, the write would block as
// soon as the TCP buffer fills up, and the handler would see neither the logout nor the shutdown.
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
