package mail

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/mail"
	"net/smtp"
	"strconv"
	"time"
)

// SMTPSender sends emails over SMTP.
//
// With port 465 it uses TLS from the start. With any other (usually 587) it connects in plain
// text and upgrades to TLS with STARTTLS, which is mandatory except against localhost (Mailpit
// in development does not use TLS). If there is a user, it authenticates with PLAIN, which
// net/smtp only allows over TLS or against localhost.
type SMTPSender struct {
	Host     string
	Port     int
	Username string
	Password string
	From     *mail.Address
}

// NewSMTPSender validates the sender (for example "My App <no-reply@myapp.com>") and creates the sender.
func NewSMTPSender(host string, port int, username, password, from string) (*SMTPSender, error) {
	addr, err := mail.ParseAddress(from)
	if err != nil {
		return nil, fmt.Errorf("invalid sender %q: %w", from, err)
	}
	return &SMTPSender{Host: host, Port: port, Username: username, Password: password, From: addr}, nil
}

// sendTimeout is the maximum time for the whole sending of an email.
const sendTimeout = 30 * time.Second

func (s *SMTPSender) Send(ctx context.Context, msg Message) error {
	raw, err := build(s.From, msg, time.Now())
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(ctx, sendTimeout)
	defer cancel()

	conn, err := s.dial(ctx)
	if err != nil {
		return fmt.Errorf("smtp: connect to %s:%d: %w", s.Host, s.Port, err)
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}

	c, err := smtp.NewClient(conn, s.Host)
	if err != nil {
		conn.Close()
		return fmt.Errorf("smtp: start the SMTP session: %w", err)
	}
	defer c.Close()

	if s.Port != 465 {
		if ok, _ := c.Extension("STARTTLS"); ok {
			if err := c.StartTLS(&tls.Config{ServerName: s.Host, MinVersion: tls.VersionTLS12}); err != nil {
				return fmt.Errorf("smtp: STARTTLS: %w", err)
			}
		} else if !isLocalhost(s.Host) {
			return errors.New("smtp: the server does not offer STARTTLS; no credentials or emails are sent unencrypted")
		}
	}

	if s.Username != "" {
		if err := c.Auth(smtp.PlainAuth("", s.Username, s.Password, s.Host)); err != nil {
			return fmt.Errorf("smtp: authentication: %w", err)
		}
	}

	if err := c.Mail(s.From.Address); err != nil {
		return fmt.Errorf("smtp: sender: %w", err)
	}
	if err := c.Rcpt(msg.To); err != nil {
		return fmt.Errorf("smtp: recipient: %w", err)
	}

	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("smtp: DATA: %w", err)
	}
	if _, err := w.Write(raw); err != nil {
		return fmt.Errorf("smtp: write the email: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("smtp: send the email: %w", err)
	}

	return c.Quit()
}

func (s *SMTPSender) dial(ctx context.Context) (net.Conn, error) {
	addr := net.JoinHostPort(s.Host, strconv.Itoa(s.Port))
	if s.Port == 465 {
		d := tls.Dialer{Config: &tls.Config{ServerName: s.Host, MinVersion: tls.VersionTLS12}}
		return d.DialContext(ctx, "tcp", addr)
	}
	var d net.Dialer
	return d.DialContext(ctx, "tcp", addr)
}

func isLocalhost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
