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

// SMTPSender envía correos por SMTP.
//
// Con el puerto 465 usa TLS desde el inicio. Con cualquier otro (normalmente 587) se conecta
// en texto plano y pasa a TLS con STARTTLS, que es obligatorio salvo contra localhost
// (Mailpit en desarrollo no usa TLS). Si hay usuario, se autentica con PLAIN, que net/smtp
// solo permite sobre TLS o contra localhost.
type SMTPSender struct {
	Host     string
	Port     int
	Username string
	Password string
	From     *mail.Address
}

// NewSMTPSender valida el remitente (por ejemplo "Mi App <no-reply@miapp.com>") y crea el sender.
func NewSMTPSender(host string, port int, username, password, from string) (*SMTPSender, error) {
	addr, err := mail.ParseAddress(from)
	if err != nil {
		return nil, fmt.Errorf("remitente inválido %q: %w", from, err)
	}
	return &SMTPSender{Host: host, Port: port, Username: username, Password: password, From: addr}, nil
}

// sendTimeout es el tiempo máximo para todo el envío de un correo.
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
		return fmt.Errorf("smtp: conectar a %s:%d: %w", s.Host, s.Port, err)
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}

	c, err := smtp.NewClient(conn, s.Host)
	if err != nil {
		conn.Close()
		return fmt.Errorf("smtp: iniciar sesión SMTP: %w", err)
	}
	defer c.Close()

	if s.Port != 465 {
		if ok, _ := c.Extension("STARTTLS"); ok {
			if err := c.StartTLS(&tls.Config{ServerName: s.Host, MinVersion: tls.VersionTLS12}); err != nil {
				return fmt.Errorf("smtp: STARTTLS: %w", err)
			}
		} else if !isLocalhost(s.Host) {
			return errors.New("smtp: el servidor no ofrece STARTTLS; no se envían credenciales ni correos sin cifrar")
		}
	}

	if s.Username != "" {
		if err := c.Auth(smtp.PlainAuth("", s.Username, s.Password, s.Host)); err != nil {
			return fmt.Errorf("smtp: autenticación: %w", err)
		}
	}

	if err := c.Mail(s.From.Address); err != nil {
		return fmt.Errorf("smtp: remitente: %w", err)
	}
	if err := c.Rcpt(msg.To); err != nil {
		return fmt.Errorf("smtp: destinatario: %w", err)
	}

	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("smtp: DATA: %w", err)
	}
	if _, err := w.Write(raw); err != nil {
		return fmt.Errorf("smtp: escribir el correo: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("smtp: enviar el correo: %w", err)
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
