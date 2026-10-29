package mail

import (
	"context"
	"sync"
)

// Recorder keeps the emails in memory instead of sending them. It is for the tests: it lets
// them check that an email was sent and read its content (for example, the link).
type Recorder struct {
	mu       sync.Mutex
	messages []Message
}

func (r *Recorder) Send(_ context.Context, msg Message) error {
	if err := msg.Validate(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.messages = append(r.messages, msg)
	return nil
}

// Messages returns the sent emails, in order.
func (r *Recorder) Messages() []Message {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Message(nil), r.messages...)
}

// Last returns the last email sent to the given address.
func (r *Recorder) Last(to string) (Message, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := len(r.messages) - 1; i >= 0; i-- {
		if r.messages[i].To == to {
			return r.messages[i], true
		}
	}
	return Message{}, false
}
