// Package mail envía correos electrónicos. Define la interfaz Sender y varias implementaciones:
//
//   - LogSender escribe el correo en el log (desarrollo, sin servidor de correo).
//   - SMTPSender lo envía por SMTP, compatible con cualquier proveedor (Resend, Brevo, SES, Mailgun…)
//     y con Mailpit en desarrollo.
//   - Queue envía en segundo plano, para no demorar la respuesta HTTP.
//   - Recorder guarda los correos en memoria, para los tests.
//
// Solo usa la librería estándar.
package mail

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"mime"
	"mime/quotedprintable"
	"net/mail"
	"strings"
	"time"
)

// Message es un correo de texto plano.
type Message struct {
	To      string // solo la dirección, por ejemplo "ana@ejemplo.com"
	Subject string
	Body    string
}

// Sender envía un correo.
type Sender interface {
	Send(ctx context.Context, msg Message) error
}

// Validate comprueba el mensaje antes de enviarlo. Rechaza los saltos de línea en el asunto
// y el destinatario: con ellos, alguien podría inyectar encabezados (por ejemplo, un Bcc).
func (m Message) Validate() error {
	if strings.ContainsAny(m.To, "\r\n") || strings.ContainsAny(m.Subject, "\r\n") {
		return errors.New("mail: el destinatario y el asunto no pueden tener saltos de línea")
	}
	addr, err := mail.ParseAddress(m.To)
	if err != nil || addr.Address != m.To {
		return fmt.Errorf("mail: destinatario inválido %q", m.To)
	}
	if strings.TrimSpace(m.Subject) == "" {
		return errors.New("mail: falta el asunto")
	}
	return nil
}

// build arma el correo en formato RFC 5322: encabezados con el asunto codificado en UTF-8,
// cuerpo en quoted-printable y fin de línea CRLF.
func build(from *mail.Address, msg Message, now time.Time) ([]byte, error) {
	if err := msg.Validate(); err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	header := func(k, v string) { fmt.Fprintf(&buf, "%s: %s\r\n", k, v) }

	header("From", from.String())
	header("To", msg.To)
	header("Subject", mime.QEncoding.Encode("utf-8", msg.Subject))
	header("Date", now.Format(time.RFC1123Z))
	header("Message-ID", messageID(from.Address))
	header("MIME-Version", "1.0")
	header("Content-Type", "text/plain; charset=utf-8")
	header("Content-Transfer-Encoding", "quoted-printable")
	buf.WriteString("\r\n")

	qp := quotedprintable.NewWriter(&buf)
	body := strings.ReplaceAll(msg.Body, "\r\n", "\n")
	if _, err := qp.Write([]byte(strings.ReplaceAll(body, "\n", "\r\n"))); err != nil {
		return nil, err
	}
	if err := qp.Close(); err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

// messageID genera un identificador único para el encabezado Message-ID, con el dominio del remitente.
func messageID(fromAddress string) string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	domain := "localhost"
	if i := strings.LastIndex(fromAddress, "@"); i >= 0 {
		domain = fromAddress[i+1:]
	}
	return fmt.Sprintf("<%s@%s>", hex.EncodeToString(b), domain)
}
