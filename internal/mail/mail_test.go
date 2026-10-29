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
		{"valid", Message{To: "alice@example.com", Subject: "Hello", Body: "x"}, true},
		{"injection in the subject", Message{To: "alice@example.com", Subject: "Hello\r\nBcc: other@example.com"}, false},
		{"injection in the recipient", Message{To: "alice@example.com\nBcc: other@example.com", Subject: "Hello"}, false},
		{"recipient with a name", Message{To: "Alice <alice@example.com>", Subject: "Hello"}, false},
		{"no subject", Message{To: "alice@example.com"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.msg.Validate(); (err == nil) != tt.ok {
				t.Errorf("Validate() = %v, want valid: %v", err, tt.ok)
			}
		})
	}
}

func TestBuild(t *testing.T) {
	from := &netmail.Address{Name: "My App", Address: "no-reply@myapp.com"}
	body := "Hi Alice,\n\nYour code is: café ñandú 123.\nA very long line " + strings.Repeat("x", 120)

	raw, err := build(from, Message{To: "alice@example.com", Subject: "Café: reset your password", Body: body}, time.Now())
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	if bytes.Contains(bytes.ReplaceAll(raw, []byte("\r\n"), nil), []byte("\n")) {
		t.Error("there are line breaks without \\r")
	}

	parsed, err := netmail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("the email cannot be read: %v", err)
	}

	subject, err := new(mime.WordDecoder).DecodeHeader(parsed.Header.Get("Subject"))
	if err != nil || subject != "Café: reset your password" {
		t.Errorf("subject = %q (%v)", subject, err)
	}
	if !strings.HasSuffix(parsed.Header.Get("Message-ID"), "@myapp.com>") {
		t.Errorf("Message-ID = %q", parsed.Header.Get("Message-ID"))
	}

	decoded, _ := io.ReadAll(quotedprintable.NewReader(parsed.Body))
	if got := strings.ReplaceAll(string(decoded), "\r\n", "\n"); got != body {
		t.Errorf("the body does not decode the same:\n%q\n%q", got, body)
	}
}

func TestSMTPSender(t *testing.T) {
	addr := os.Getenv("SMTP_TEST_ADDR")
	if addr == "" {
		t.Skip("SMTP_TEST_ADDR is not set")
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
	msg := Message{To: "test@example.com", Subject: "SMTP test " + time.Now().Format(time.TimeOnly), Body: "Hello from the tests.\nÑandú."}
	if err := s.Send(t.Context(), msg); err != nil {
		t.Fatalf("Send: %v", err)
	}
}

// Every implementation satisfies Sender: if one changes its signature, the package does not compile.
var (
	_ Sender = (*Recorder)(nil)
	_ Sender = LogSender{}
	_ Sender = (*SMTPSender)(nil)
)
