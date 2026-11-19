package mail

import (
	"bufio"
	"bytes"
	"io"
	"mime"
	"mime/multipart"
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

func TestBuildWithHTML(t *testing.T) {
	from := &netmail.Address{Name: "My App", Address: "no-reply@myapp.com"}
	msg := Message{To: "alice@example.com", Subject: "Hello", Body: "Hi Alice,\nñandú", HTML: "<p>Hi Alice,<br>ñandú</p>"}
	raw, err := build(from, msg, time.Now())
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	parsed, err := netmail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("the email cannot be read: %v", err)
	}
	mediaType, params, err := mime.ParseMediaType(parsed.Header.Get("Content-Type"))
	if err != nil || mediaType != "multipart/alternative" {
		t.Fatalf("Content-Type = %q (%v)", parsed.Header.Get("Content-Type"), err)
	}

	// The plain text first and the HTML after: clients show the last one they understand.
	reader := multipart.NewReader(parsed.Body, params["boundary"])
	for _, want := range []struct{ contentType, body string }{
		{"text/plain; charset=utf-8", "Hi Alice,\r\nñandú"},
		{"text/html; charset=utf-8", msg.HTML},
	} {
		part, err := reader.NextRawPart()
		if err != nil {
			t.Fatalf("part %s: %v", want.contentType, err)
		}
		decoded, _ := io.ReadAll(quotedprintable.NewReader(part))
		if part.Header.Get("Content-Type") != want.contentType || string(decoded) != want.body {
			t.Errorf("part %q = %q", part.Header.Get("Content-Type"), decoded)
		}
	}
	if _, err := reader.NextPart(); err != io.EOF {
		t.Errorf("want only two parts: %v", err)
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
	msg := Message{
		To:      "test@example.com",
		Subject: "SMTP test " + time.Now().Format(time.TimeOnly),
		Body:    "Hello from the tests.\nÑandú.",
		HTML:    "<p>Hello from the <strong>tests</strong>.<br>Ñandú.</p>",
	}
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

// A long subject goes in several lines, and reads the same when they are joined back.
func TestBuildFoldsLongSubject(t *testing.T) {
	from, _ := netmail.ParseAddress("My App <no-reply@example.com>")
	for _, subject := range []string{
		strings.Repeat("Confirm your account ", 60),      // ASCII: it is not encoded
		strings.Repeat("Confirmación de tu cuenta ", 12), // encoded in several words
	} {
		subject = strings.TrimSpace(subject)
		raw, err := build(from, Message{To: "alice@example.com", Subject: subject, Body: "hello"}, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		// 78 is what a line should have at most (998, what it can). The first one can go a bit over: an
		// encoded word has up to 75 characters, after "Subject: ".
		for line := range strings.SplitSeq(string(raw), "\r\n") {
			if len(line) > 78+len("Subject: ") {
				t.Errorf("a line of %d characters: %.60s…", len(line), line)
			}
		}
		parsed, err := netmail.ReadMessage(bytes.NewReader(raw))
		if err != nil {
			t.Fatal(err)
		}
		if got, err := new(mime.WordDecoder).DecodeHeader(parsed.Header.Get("Subject")); err != nil || got != subject {
			t.Errorf("subject = %q (%v), want %q", got, err, subject)
		}
	}
}

// The server accepts the email and hangs up without waiting for the QUIT: the email was sent, so
// Send must not return an error (the worker would send it again).
func TestSMTPSenderIgnoresAFailedGoodbye(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })

	accepted := make(chan bool, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		reply := func(s string) { _, _ = io.WriteString(conn, s+"\r\n") }
		r := bufio.NewReader(conn)
		reply("220 test")
		data := false
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			switch cmd := strings.ToUpper(strings.TrimSpace(line)); {
			case data && cmd == ".":
				reply("250 accepted")
				accepted <- true
				return // hangs up: the QUIT finds the connection closed
			case data:
			case strings.HasPrefix(cmd, "EHLO"):
				reply("250 test")
			case cmd == "DATA":
				reply("354 go ahead")
				data = true
			default: // MAIL FROM, RCPT TO
				reply("250 ok")
			}
		}
	}()

	_, portStr, _ := net.SplitHostPort(ln.Addr().String())
	port, _ := strconv.Atoi(portStr)
	s, err := NewSMTPSender("127.0.0.1", port, "", "", "My App <no-reply@example.com>")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Send(t.Context(), Message{To: "alice@example.com", Subject: "Hello", Body: "hello"}); err != nil {
		t.Errorf("Send: %v, want nil: the server had accepted the email", err)
	}
	select {
	case <-accepted:
	default:
		t.Error("the server never got the email")
	}
}
