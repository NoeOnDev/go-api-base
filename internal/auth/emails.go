package auth

import (
	"bytes"
	"context"
	"embed"
	"fmt"
	"net/url"
	"text/template"

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
}

// sendEmail arma el correo con la plantilla indicada y lo envía. Con mail.Queue el envío
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
