package mail

import (
	"context"
	"log/slog"
	"sync"
)

// Queue envía los correos en segundo plano: Send valida el mensaje y vuelve enseguida,
// y el envío real ocurre en otra goroutine. Así la respuesta HTTP no espera al servidor
// de correo, y tampoco tarda distinto según se envíe un correo o no (por ejemplo, al pedir
// recuperar la contraseña de un email que no existe).
//
// Los errores de envío se registran en el log. Al apagar la aplicación, Wait espera
// a que terminen los envíos pendientes.
type Queue struct {
	sender Sender
	logger *slog.Logger
	wg     sync.WaitGroup
}

// NewQueue envuelve un Sender para enviar en segundo plano.
func NewQueue(sender Sender, logger *slog.Logger) *Queue {
	return &Queue{sender: sender, logger: logger}
}

func (q *Queue) Send(ctx context.Context, msg Message) error {
	if err := msg.Validate(); err != nil {
		return err
	}

	q.wg.Add(1)
	go func() {
		defer q.wg.Done()
		// El envío no debe cancelarse cuando termina la petición HTTP que lo originó.
		if err := q.sender.Send(context.WithoutCancel(ctx), msg); err != nil {
			q.logger.Error("no se pudo enviar el correo", "to", msg.To, "subject", msg.Subject, "error", err)
			return
		}
		q.logger.Info("correo enviado", "to", msg.To, "subject", msg.Subject)
	}()
	return nil
}

// Wait espera a que terminen los envíos pendientes, o a que ctx venza.
func (q *Queue) Wait(ctx context.Context) error {
	done := make(chan struct{})
	go func() {
		q.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
