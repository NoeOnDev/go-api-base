package mail

import (
	"bytes"
	"io"
	"mime"
	"mime/quotedprintable"
	"net"
	netmail "net/mail"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestMessageValidate(t *testing.T) {
	tests := []struct {
		name string
		msg  Message
		ok   bool
	}{
		{"válido", Message{To: "ana@ejemplo.com", Subject: "Hola", Body: "x"}, true},
		{"inyección en el asunto", Message{To: "ana@ejemplo.com", Subject: "Hola\r\nBcc: otro@ejemplo.com"}, false},
		{"inyección en el destinatario", Message{To: "ana@ejemplo.com\nBcc: otro@ejemplo.com", Subject: "Hola"}, false},
		{"destinatario con nombre", Message{To: "Ana <ana@ejemplo.com>", Subject: "Hola"}, false},
		{"sin asunto", Message{To: "ana@ejemplo.com"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.msg.Validate(); (err == nil) != tt.ok {
				t.Errorf("Validate() = %v, se esperaba válido: %v", err, tt.ok)
			}
		})
	}
}

func TestBuild(t *testing.T) {
	from := &netmail.Address{Name: "Mi App", Address: "no-reply@miapp.com"}
	body := "Hola Ana,\n\nTu código es: ñandú 123.\nUna línea muy larga " + strings.Repeat("x", 120)

	raw, err := build(from, Message{To: "ana@ejemplo.com", Subject: "Recuperá tu contraseña", Body: body}, time.Now())
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	if bytes.Contains(bytes.ReplaceAll(raw, []byte("\r\n"), nil), []byte("\n")) {
		t.Error("hay saltos de línea sin \\r")
	}

	parsed, err := netmail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("el correo no se puede leer: %v", err)
	}

	subject, err := new(mime.WordDecoder).DecodeHeader(parsed.Header.Get("Subject"))
	if err != nil || subject != "Recuperá tu contraseña" {
		t.Errorf("asunto = %q (%v)", subject, err)
	}
	if !strings.HasSuffix(parsed.Header.Get("Message-ID"), "@miapp.com>") {
		t.Errorf("Message-ID = %q", parsed.Header.Get("Message-ID"))
	}

	decoded, _ := io.ReadAll(quotedprintable.NewReader(parsed.Body))
	if got := strings.ReplaceAll(string(decoded), "\r\n", "\n"); got != body {
		t.Errorf("el cuerpo no se decodifica igual:\n%q\n%q", got, body)
	}
}

func TestSMTPSender(t *testing.T) {
	addr := os.Getenv("SMTP_TEST_ADDR")
	if addr == "" {
		t.Skip("SMTP_TEST_ADDR no está definida")
	}
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatal(err)
	}
	port, _ := strconv.Atoi(portStr)

	s, err := NewSMTPSender(host, port, "", "", "Go API Base <no-reply@go-api-base.test>")
	if err != nil {
		t.Fatal(err)
	}
	msg := Message{To: "prueba@ejemplo.com", Subject: "Prueba de SMTP " + time.Now().Format(time.TimeOnly), Body: "Hola desde los tests.\nÑandú."}
	if err := s.Send(t.Context(), msg); err != nil {
		t.Fatalf("Send: %v", err)
	}
}

// Todas las implementaciones cumplen Sender: si una cambia de firma, el paquete no compila.
var (
	_ Sender = (*Recorder)(nil)
	_ Sender = LogSender{}
	_ Sender = (*SMTPSender)(nil)
)
