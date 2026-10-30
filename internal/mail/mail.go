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
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"net/textproto"
	"strings"
	"time"
)

// Message is an email: its plain-text body and, optionally, the HTML one. With both, clients
// show the HTML, and the text is for the ones without HTML (and helps against spam filters).
type Message struct {
	To      string // only the address, for example "alice@example.com"
	Subject string
	Body    string // plain text
	HTML    string // optional
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
// quoted-printable body and CRLF line endings. With HTML, the body is multipart/alternative: the
// plain text first and the HTML after, so clients show the last one they understand.
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

	if msg.HTML == "" {
		header("Content-Type", "text/plain; charset=utf-8")
		header("Content-Transfer-Encoding", "quoted-printable")
		buf.WriteString("\r\n")
		if err := writeQuotedPrintable(&buf, msg.Body); err != nil {
			return nil, err
		}
		return buf.Bytes(), nil
	}

	mw := multipart.NewWriter(&buf)
	header("Content-Type", mime.FormatMediaType("multipart/alternative", map[string]string{"boundary": mw.Boundary()}))
	buf.WriteString("\r\n")
	for _, part := range []struct{ contentType, body string }{
		{"text/plain; charset=utf-8", msg.Body},
		{"text/html; charset=utf-8", msg.HTML},
	} {
		w, err := mw.CreatePart(textproto.MIMEHeader{
			"Content-Type":              {part.contentType},
			"Content-Transfer-Encoding": {"quoted-printable"},
		})
		if err != nil {
			return nil, err
		}
		if err := writeQuotedPrintable(w, part.body); err != nil {
			return nil, err
		}
	}
	if err := mw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// writeQuotedPrintable writes body in quoted-printable, with CRLF line endings.
func writeQuotedPrintable(w io.Writer, body string) error {
	qp := quotedprintable.NewWriter(w)
	body = strings.ReplaceAll(body, "\r\n", "\n")
	if _, err := qp.Write([]byte(strings.ReplaceAll(body, "\n", "\r\n"))); err != nil {
		return err
	}
	return qp.Close()
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
