package mail

import (
	"context"
	"log/slog"
)

// LogSender no envía nada: escribe el correo en el log. Es útil en desarrollo cuando no hay
// servidor de correo; los enlaces (por ejemplo, para recuperar la contraseña) se copian del log.
// No usarlo en producción: el log quedaría con enlaces válidos.
type LogSender struct {
	Logger *slog.Logger
}

func (s LogSender) Send(ctx context.Context, msg Message) error {
	if err := msg.Validate(); err != nil {
		return err
	}
	s.Logger.InfoContext(ctx, "correo (no enviado, MAIL_DRIVER=log)",
		"to", msg.To, "subject", msg.Subject, "body", msg.Body)
	return nil
}
