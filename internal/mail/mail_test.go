package mail

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
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

func TestQueueSendsInBackgroundAndWaits(t *testing.T) {
	slow := senderFunc(func(ctx context.Context, msg Message) error {
		time.Sleep(50 * time.Millisecond)
		return (&recorder).Send(ctx, msg)
	})
	q := NewQueue(slow, slog.New(slog.DiscardHandler))

	start := time.Now()
	if err := q.Send(t.Context(), Message{To: "ana@ejemplo.com", Subject: "Hola"}); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > 20*time.Millisecond {
		t.Error("Send debería volver sin esperar el envío")
	}

	if err := q.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, ok := recorder.Last("ana@ejemplo.com"); !ok {
		t.Error("el correo no se envió antes de que Wait volviera")
	}

	if err := q.Send(t.Context(), Message{To: "inválido", Subject: "x"}); err == nil {
		t.Error("Send debería validar el mensaje antes de encolarlo")
	}
}

func TestQueueLogsErrors(t *testing.T) {
	var logs bytes.Buffer
	failing := senderFunc(func(context.Context, Message) error { return errors.New("servidor caído") })
	q := NewQueue(failing, slog.New(slog.NewTextHandler(&logs, nil)))

	_ = q.Send(t.Context(), Message{To: "ana@ejemplo.com", Subject: "Hola"})
	_ = q.Wait(t.Context())

	if !strings.Contains(logs.String(), "servidor caído") {
		t.Errorf("el error no quedó en el log: %s", logs.String())
	}
}

// TestSMTPSender envía un correo real. Solo corre si SMTP_TEST_ADDR apunta a un servidor
// de pruebas, por ejemplo Mailpit: SMTP_TEST_ADDR=localhost:1025 go test ./internal/mail/
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

var recorder Recorder

type senderFunc func(context.Context, Message) error

func (f senderFunc) Send(ctx context.Context, msg Message) error { return f(ctx, msg) }
