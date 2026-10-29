// Package mail sends emails. It defines the Sender interface and several implementations:
//
//   - LogSender writes the email to the log (development, without a mail server).
//   - SMTPSender sends it over SMTP, compatible with any provider (Resend, Brevo, SES, Mailgun…)
//     and with Mailpit in development.
//   - Recorder keeps the emails in memory, for the tests.
//
// In the application, the handlers do not send with these senders: they queue the email with
// jobs.MailSender, so the HTTP response is not delayed and it is retried if it fails, and the
// queue worker sends it with SMTPSender or LogSender.
//
// It uses only the standard library.
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

// Message is a plain-text email.
type Message struct {
	To      string // only the address, for example "alice@example.com"
	Subject string
	Body    string
}

// Sender sends an email.
type Sender interface {
	Send(ctx context.Context, msg Message) error
}

// Validate checks the message before sending it. It rejects line breaks in the subject and the
// recipient: with them, someone could inject headers (for example, a Bcc).
func (m Message) Validate() error {
	if strings.ContainsAny(m.To, "\r\n") || strings.ContainsAny(m.Subject, "\r\n") {
		return errors.New("mail: the recipient and the subject cannot have line breaks")
	}
	addr, err := mail.ParseAddress(m.To)
	if err != nil || addr.Address != m.To {
		return fmt.Errorf("mail: invalid recipient %q", m.To)
	}
	if strings.TrimSpace(m.Subject) == "" {
		return errors.New("mail: missing subject")
	}
	return nil
}

// build assembles the email in RFC 5322 format: headers with the subject encoded in UTF-8,
// quoted-printable body and CRLF line endings.
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

// messageID generates a unique identifier for the Message-ID header, with the sender's domain.
func messageID(fromAddress string) string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	domain := "localhost"
	if i := strings.LastIndex(fromAddress, "@"); i >= 0 {
		domain = fromAddress[i+1:]
	}
	return fmt.Sprintf("<%s@%s>", hex.EncodeToString(b), domain)
}
