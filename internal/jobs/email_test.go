package jobs

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"maps"
	"strings"
	"testing"

	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivertest"
	"github.com/riverqueue/river/rivertype"

	"github.com/NoeOnDev/go-api-base/internal/mail"
	"github.com/NoeOnDev/go-api-base/internal/secretbox"
	"github.com/NoeOnDev/go-api-base/internal/store"
	"github.com/NoeOnDev/go-api-base/internal/testutil"
)

func testBox(t *testing.T) *secretbox.Box {
	t.Helper()
	box, err := secretbox.New(testutil.EncryptionKey)
	if err != nil {
		t.Fatal(err)
	}
	return box
}

var resetEmail = mail.Message{
	To:      "alice@example.com",
	Subject: "Recuperá tu contraseña",
	Body:    "Abrí https://app.test/reset-password?token=token-secreto",
}

func TestMailSenderEncryptsQueuedEmail(t *testing.T) {
	pool := testutil.NewDB(t)
	box := testBox(t)
	client, err := New(pool, slog.New(slog.DiscardHandler), Config{MaxWorkers: 1}, Deps{Queries: store.New(pool), Mailer: &mail.Recorder{}, Box: box})
	if err != nil {
		t.Fatal(err)
	}

	if err := NewMailSender(client, box).Send(t.Context(), resetEmail); err != nil {
		t.Fatal(err)
	}

	// Lo que queda en la base no deja ver ni el token ni el destinatario.
	var raw string
	if err := pool.QueryRow(t.Context(), "SELECT args::text FROM river_job WHERE kind = 'send_email'").Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, "token-secreto") || strings.Contains(raw, "alice@example.com") {
		t.Errorf("el correo encolado no está cifrado: %s", raw)
	}

	job := rivertest.RequireInserted(t.Context(), t, riverpgxv5.New(pool), &SendEmailArgs{}, &rivertest.RequireInsertedOpts{Queue: QueueEmail, MaxAttempts: 5})
	if _, err := box.Decrypt(job.Args.Message, emailAAD); err != nil {
		t.Errorf("el correo encolado no se puede descifrar: %v", err)
	}

	// Un mensaje inválido se rechaza al encolar, no en el worker.
	if err := NewMailSender(client, box).Send(t.Context(), mail.Message{To: "no es un email"}); err == nil {
		t.Error("se esperaba un error de validación")
	}
}

// workEmail encola el mensaje cifrado y lo procesa con el worker, como lo haría River.
// Devuelve el error del worker, si hubo.
func workEmail(t *testing.T, sender mail.Sender, message string) (*rivertest.WorkResult, error) {
	t.Helper()
	pool := testutil.NewDB(t)
	box := testBox(t)
	worker := rivertest.NewWorker(t, riverpgxv5.New(pool), &river.Config{}, &sendEmailWorker{sender: sender, box: box})

	if message == "" {
		plain := `{"To":"alice@example.com","Subject":"Recuperá tu contraseña","Body":"hola"}`
		enc, err := box.Encrypt([]byte(plain), emailAAD)
		if err != nil {
			t.Fatal(err)
		}
		message = enc
	}

	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })
	return worker.Work(t.Context(), t, tx, SendEmailArgs{Message: message}, nil)
}

func TestSendEmailWorker(t *testing.T) {
	recorder := &mail.Recorder{}
	res, err := workEmail(t, recorder, "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Job.State != rivertype.JobStateCompleted {
		t.Errorf("estado = %s", res.Job.State)
	}
	if msg, ok := recorder.Last("alice@example.com"); !ok || msg.Subject != "Recuperá tu contraseña" {
		t.Errorf("no se envió el correo: %+v", recorder.Messages())
	}
}

type failingSender struct{}

func (failingSender) Send(context.Context, mail.Message) error {
	return errors.New("servidor SMTP caído")
}

func TestSendEmailWorkerRetriesOnFailure(t *testing.T) {
	// Si el SMTP falla, el trabajo queda para reintentarse más tarde.
	res, err := workEmail(t, failingSender{}, "")
	if err == nil {
		t.Fatal("se esperaba el error del SMTP")
	}
	// Queda retryable, o available si el primer reintento es casi inmediato: en los dos casos,
	// River lo vuelve a intentar. Lo que no puede pasar es que quede descartado o completo.
	retried := res.Job.State == rivertype.JobStateRetryable || res.Job.State == rivertype.JobStateAvailable
	if !retried || len(res.Job.Errors) != 1 || res.Job.Attempt >= res.Job.MaxAttempts {
		t.Errorf("el correo debería reintentarse: estado %s, intento %d de %d, errores %d",
			res.Job.State, res.Job.Attempt, res.Job.MaxAttempts, len(res.Job.Errors))
	}
}

func TestSendEmailWorkerCancelsUndecryptable(t *testing.T) {
	// Un correo que no se puede descifrar no se reintenta: no serviría de nada.
	res, _ := workEmail(t, &mail.Recorder{}, "00000000:AAAA")
	if res.Job.State != rivertype.JobStateCancelled {
		t.Errorf("estado = %s, se esperaba cancelled", res.Job.State)
	}
}

// QueuedEmailKeys cuenta los correos que todavía no salieron, por la clave con la que se cifraron.
func TestQueuedEmailKeys(t *testing.T) {
	pool := testutil.NewDB(t)
	boxA := testBox(t)
	boxB, err := secretbox.New(bytes.Repeat([]byte{2}, 32), testutil.EncryptionKey) // B actual, A anterior
	if err != nil {
		t.Fatal(err)
	}
	client, err := New(pool, slog.New(slog.DiscardHandler), Config{MaxWorkers: 1}, Deps{Queries: store.New(pool), Mailer: &mail.Recorder{}, Box: boxB})
	if err != nil {
		t.Fatal(err)
	}

	// Uno con cada clave. El cliente no arrancó, así que quedan esperando.
	for _, box := range []*secretbox.Box{boxA, boxB} {
		if err := NewMailSender(client, box).Send(t.Context(), resetEmail); err != nil {
			t.Fatal(err)
		}
	}
	// Uno cancelado ya no está en la cola: no cuenta.
	enc, err := boxA.Encrypt([]byte(`{"To":"alice@example.com","Subject":"Hola","Body":"hola"}`), emailAAD)
	if err != nil {
		t.Fatal(err)
	}
	inserted, err := client.Insert(t.Context(), SendEmailArgs{Message: enc}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.JobCancel(t.Context(), inserted.Job.ID); err != nil {
		t.Fatal(err)
	}

	got, err := QueuedEmailKeys(t.Context(), pool)
	if err != nil {
		t.Fatal(err)
	}
	if want := map[string]int64{boxA.KeyID(): 1, boxB.KeyID(): 1}; !maps.Equal(got, want) {
		t.Errorf("QueuedEmailKeys = %v, se esperaba %v", got, want)
	}
}
