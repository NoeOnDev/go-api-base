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

// Las plantillas de los correos están en emails/<idioma>/*.txt y van embebidas en el binario.
// Cada una define su asunto con {{define "subject"}}. Para personalizarlas, editá esos archivos;
// para sumar un idioma, ver docs/i18n.md.
//
//go:embed emails
var emailFiles embed.FS

// newEmailTemplates carga las plantillas. Van embebidas, así que un error es del código (lo
// detectan los tests), no de la configuración.
func newEmailTemplates(catalog *i18n.Catalog) *mail.Templates {
	root, err := fs.Sub(emailFiles, "emails")
	if err == nil {
		var t *mail.Templates
		if t, err = mail.NewTemplates(root, catalog); err == nil {
			return t
		}
	}
	panic(fmt.Sprintf("auth: plantillas de correo: %v", err))
}

// emailData son los datos disponibles en las plantillas.
type emailData struct {
	Name    string
	Link    string
	Expires string // por ejemplo "1 hour", en el idioma del correo (ver expiresIn)
	When    string // fecha y hora del evento, en UTC
	IP      string // IP desde la que se hizo el pedido
	Email   string // otro email que se menciona (por ejemplo, el nuevo en un cambio de email)
}

// Límite de correos por destinatario y tipo: 3 seguidos y luego uno cada 20 minutos.
// Complementa el límite por IP: desde muchas IPs tampoco se le pueden mandar
// decenas de correos a una misma persona.
const (
	emailLimitEvery = 20 * time.Minute
	emailLimitBurst = 3
)

// errTooManyEmails responde cuando el propio usuario pide más correos de los permitidos
// (reenviar la verificación, cambiar el email): ahí no hay nada que ocultar.
var errTooManyEmails = &httpx.Error{Status: http.StatusTooManyRequests, Code: "too_many_emails"}

// allowEmail indica si se le puede enviar otro correo de este tipo al usuario.
// Cada tipo (recuperación, verificación, aviso) tiene su propio límite.
func (s *Service) allowEmail(ctx context.Context, kind string, userID int64) (bool, error) {
	return s.emailLimiter.Allow(ctx, kind+":"+strconv.FormatInt(userID, 10))
}

// sendEmail arma el correo con la plantilla indicada, en el idioma del usuario (locale; nil es el
// idioma por defecto), y lo envía. Va en el idioma del dueño de la cuenta y no en el de la
// petición: un aviso de seguridad tiene que entenderlo él, aunque el cambio lo haya hecho otro.
// Con jobs.MailSender el envío ocurre en segundo plano, así que esto no demora la respuesta.
func (s *Service) sendEmail(ctx context.Context, to string, locale *string, tmpl string, data emailData) error {
	subject, body, err := s.emails.Render(deref(locale), tmpl, data)
	if err != nil {
		return fmt.Errorf("armar el correo %s: %w", tmpl, err)
	}
	if err := s.opts.Mailer.Send(ctx, mail.Message{To: to, Subject: subject, Body: body}); err != nil {
		return fmt.Errorf("enviar el correo %s: %w", tmpl, err)
	}
	return nil
}

// expiresIn es la vigencia de un enlace en el idioma del usuario, por ejemplo "24 hours".
func (s *Service) expiresIn(locale *string, ttl time.Duration) string {
	return s.catalog.Translator(deref(locale)).Plural("duration.hours", int(ttl.Hours()))
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// frontendLink arma el enlace a una pantalla del frontend con el token, por ejemplo
// https://app.cliente.com/reset-password?token=…
func (s *Service) frontendLink(path, token string) string {
	return s.opts.FrontendURL + path + "?token=" + url.QueryEscape(token)
}
