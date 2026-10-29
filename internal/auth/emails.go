package auth

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/NoeOnDev/go-api-base/internal/httpx"
	"github.com/NoeOnDev/go-api-base/internal/i18n"
	"github.com/NoeOnDev/go-api-base/internal/mail"
)

// The email templates are in emails/<language>/*.txt and are embedded in the binary. Each one
// sets its subject with {{define "subject"}}. To customize them, edit those files; to add a
// language, see docs/i18n.md.
//
//go:embed emails
var emailFiles embed.FS

// newEmailTemplates loads the templates. They are embedded, so an error is a code error (the
// tests catch it), not a configuration one.
func newEmailTemplates(catalog *i18n.Catalog) *mail.Templates {
	root, err := fs.Sub(emailFiles, "emails")
	if err == nil {
		var t *mail.Templates
		if t, err = mail.NewTemplates(root, catalog); err == nil {
			return t
		}
	}
	panic(fmt.Sprintf("auth: email templates: %v", err))
}

// emailData is the data available to the templates.
type emailData struct {
	Name    string
	Link    string
	Expires string // for example "1 hour", in the language of the email (see expiresIn)
	When    string // date and time of the event, in UTC
	IP      string // IP the request came from
	Email   string // another email mentioned (for example, the new one in an email change)
}

// Limit of emails per recipient and kind: 3 in a row and then one every 20 minutes. It
// complements the per-IP limit: not even from many IPs can someone send dozens of emails to
// the same person.
const (
	emailLimitEvery = 20 * time.Minute
	emailLimitBurst = 3
)

// errTooManyEmails answers when the user themself asks for more emails than allowed (resending
// the verification, changing the email): there is nothing to hide there.
var errTooManyEmails = &httpx.Error{Status: http.StatusTooManyRequests, Code: "too_many_emails"}

// allowEmail reports whether another email of this kind can be sent to the user.
// Each kind (reset, verification, notice) has its own limit.
func (s *Service) allowEmail(ctx context.Context, kind string, userID int64) (bool, error) {
	return s.emailLimiter.Allow(ctx, kind+":"+strconv.FormatInt(userID, 10))
}

// sendEmail builds the email with the given template, in the user's language (locale; nil is
// the default language), and sends it. It goes in the language of the account owner and not in
// that of the request: a security notice must be understood by the owner, even if someone else
// made the change. With jobs.MailSender the sending happens in the background, so this does not
// delay the response.
func (s *Service) sendEmail(ctx context.Context, to string, locale *string, tmpl string, data emailData) error {
	subject, body, err := s.emails.Render(deref(locale), tmpl, data)
	if err != nil {
		return fmt.Errorf("build the email %s: %w", tmpl, err)
	}
	if err := s.opts.Mailer.Send(ctx, mail.Message{To: to, Subject: subject, Body: body}); err != nil {
		return fmt.Errorf("send the email %s: %w", tmpl, err)
	}
	return nil
}

// expiresIn is how long a link lasts, in the user's language, for example "24 hours".
func (s *Service) expiresIn(locale *string, ttl time.Duration) string {
	return s.catalog.Translator(deref(locale)).Plural("duration.hours", int(ttl.Hours()))
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// frontendLink builds the link to a frontend screen with the token, for example
// https://app.client.com/reset-password?token=…
func (s *Service) frontendLink(path, token string) string {
	return s.opts.FrontendURL + path + "?token=" + url.QueryEscape(token)
}
