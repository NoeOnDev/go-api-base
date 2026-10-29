package auth

import (
	"bytes"
	"context"
	"embed"
	"fmt"
	"net/url"
	"strconv"
	"text/template"
	"time"

	"github.com/NoeOnDev/go-api-base/internal/mail"
)

// Las plantillas de los correos están en emails/*.txt y van embebidas en el binario.
// Para personalizarlas, editá esos archivos.
//
//go:embed emails/*.txt
var emailFiles embed.FS

var emailTemplates = template.Must(template.ParseFS(emailFiles, "emails/*.txt"))

// emailData son los datos disponibles en las plantillas.
type emailData struct {
	Name    string
	Link    string
	Expires string // por ejemplo "1 hora"
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

// allowEmail indica si se le puede enviar otro correo de este tipo al usuario.
// Cada tipo (recuperación, verificación, aviso) tiene su propio límite.
func (s *Service) allowEmail(ctx context.Context, kind string, userID int64) (bool, error) {
	return s.emailLimiter.Allow(ctx, kind+":"+strconv.FormatInt(userID, 10))
}

// sendEmail arma el correo con la plantilla indicada y lo envía. Con jobs.MailSender el envío
// ocurre en segundo plano, así que esto no demora la respuesta.
func (s *Service) sendEmail(ctx context.Context, to, subject, tmpl string, data emailData) error {
	var body bytes.Buffer
	if err := emailTemplates.ExecuteTemplate(&body, tmpl, data); err != nil {
		return fmt.Errorf("armar el correo %s: %w", tmpl, err)
	}
	if err := s.opts.Mailer.Send(ctx, mail.Message{To: to, Subject: subject, Body: body.String()}); err != nil {
		return fmt.Errorf("enviar el correo %s: %w", tmpl, err)
	}
	return nil
}

// frontendLink arma el enlace a una pantalla del frontend con el token, por ejemplo
// https://app.cliente.com/reset-password?token=…
func (s *Service) frontendLink(path, token string) string {
	return s.opts.FrontendURL + path + "?token=" + url.QueryEscape(token)
}
