package mail

import (
	"context"
	"sync"
)

// Recorder guarda los correos en memoria en lugar de enviarlos. Es para los tests:
// permite comprobar que se envió un correo y leer su contenido (por ejemplo, el enlace).
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

// Messages devuelve los correos enviados, en orden.
func (r *Recorder) Messages() []Message {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Message(nil), r.messages...)
}

// Last devuelve el último correo enviado a la dirección indicada.
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
