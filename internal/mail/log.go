package mail

import (
	"context"
	"log/slog"
)

// LogSender sends nothing: it writes the email to the log. It is useful in development when
// there is no mail server; the links (for example, to reset the password) are copied from the log.
// Do not use it in production: the log would hold valid links.
type LogSender struct {
	Logger *slog.Logger
}

func (s LogSender) Send(ctx context.Context, msg Message) error {
	if err := msg.Validate(); err != nil {
		return err
	}
	s.Logger.InfoContext(ctx, "email (not sent, MAIL_DRIVER=log)",
		"to", msg.To, "subject", msg.Subject, "body", msg.Body)
	return nil
}
