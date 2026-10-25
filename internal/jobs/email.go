package jobs

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/riverqueue/river"

	"github.com/NoeOnDev/go-api-base/internal/mail"
	"github.com/NoeOnDev/go-api-base/internal/secretbox"
)

// SendEmailArgs es el trabajo de enviar un correo. El mensaje entero (destinatario, asunto
// y cuerpo) va cifrado: los correos de recuperación llevan un enlace con token, y sin cifrar,
// quien lea la tabla river_job podría usar un enlace vigente.
type SendEmailArgs struct {
	Message string `json:"message"` // mail.Message en JSON, cifrado con secretbox
}

func (SendEmailArgs) Kind() string { return "send_email" }

// InsertOpts manda el correo a su cola, con hasta 5 intentos. Entre intento e intento River
// espera cada vez más (segundos, luego minutos), así una caída corta del SMTP no pierde correos.
func (SendEmailArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: QueueEmail, MaxAttempts: 5}
}

// emailAAD ata el mensaje cifrado a su uso: no se puede descifrar como otro tipo de dato.
var emailAAD = []byte("jobs.send_email")

// MailSender es el mail.Sender de producción: en lugar de enviar, encola el correo. Así la
// respuesta HTTP no espera al servidor de correo, y si este falla o la API se reinicia,
// el correo no se pierde.
type MailSender struct {
	client *Client
	box    *secretbox.Box
}

// NewMailSender crea el sender que encola en client, cifrando con box.
func NewMailSender(client *Client, box *secretbox.Box) *MailSender {
	return &MailSender{client: client, box: box}
}

func (s *MailSender) Send(ctx context.Context, msg mail.Message) error {
	if err := msg.Validate(); err != nil {
		return err
	}
	plain, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("codificar correo: %w", err)
	}
	encrypted, err := s.box.Encrypt(plain, emailAAD)
	if err != nil {
		return fmt.Errorf("cifrar correo: %w", err)
	}
	if _, err := s.client.Insert(ctx, SendEmailArgs{Message: encrypted}, nil); err != nil {
		return fmt.Errorf("encolar correo: %w", err)
	}
	return nil
}

// sendEmailWorker envía los correos encolados con el sender real (SMTP o log).
type sendEmailWorker struct {
	river.WorkerDefaults[SendEmailArgs]
	sender mail.Sender
	box    *secretbox.Box
}

func (w *sendEmailWorker) Work(ctx context.Context, job *river.Job[SendEmailArgs]) error {
	plain, err := w.box.Decrypt(job.Args.Message, emailAAD)
	if err != nil {
		// Reintentar no sirve: falta la clave o el dato está dañado.
		return river.JobCancel(fmt.Errorf("descifrar correo: %w", err))
	}
	var msg mail.Message
	if err := json.Unmarshal(plain, &msg); err != nil {
		return river.JobCancel(fmt.Errorf("decodificar correo: %w", err))
	}
	return w.sender.Send(ctx, msg)
}
