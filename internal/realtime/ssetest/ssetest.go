// Package ssetest reads SSE streams in the tests, as EventSource would in the browser.
package ssetest

import (
	"bufio"
	"io"
	"strings"
	"testing"
	"time"
)

// Event is an event read from the stream. Comment has the text of a comment (": ping").
type Event struct {
	ID, Type, Data, Comment string
}

// Reader reads events from an SSE stream in another goroutine.
type Reader struct {
	events chan Event
	done   chan struct{}
}

// NewReader starts reading the body of an SSE response.
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

// Next returns the next event, or fails the test if it does not arrive before the timeout.
// It skips comments unless asked for with NextComment.
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
			t.Fatal("the stream closed before the event arrived")
		case <-deadline:
			t.Fatal("no event arrived in time")
		}
	}
}

// NextComment returns the next comment (for example, the heartbeat).
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
			t.Fatal("the stream closed before the comment arrived")
		case <-deadline:
			t.Fatal("no comment arrived in time")
		}
	}
}

// Nothing checks that no event arrives during the given time.
func (r *Reader) Nothing(t *testing.T, wait time.Duration) {
	t.Helper()
	deadline := time.After(wait)
	for {
		select {
		case ev := <-r.events:
			if ev.Comment == "" {
				t.Fatalf("an unexpected event arrived: %+v", ev)
			}
		case <-deadline:
			return
		}
	}
}

// Closed reports whether the stream ended before the timeout.
func (r *Reader) Closed(timeout time.Duration) bool {
	select {
	case <-r.done:
		return true
	case <-time.After(timeout):
		return false
	}
}
