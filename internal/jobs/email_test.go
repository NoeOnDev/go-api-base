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
	Subject: "Reset your password",
	Body:    "Open https://app.test/reset-password?token=secret-token",
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

	// What stays in the database reveals neither the token nor the recipient.
	var raw string
	if err := pool.QueryRow(t.Context(), "SELECT args::text FROM river_job WHERE kind = 'send_email'").Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, "secret-token") || strings.Contains(raw, "alice@example.com") {
		t.Errorf("the queued email is not encrypted: %s", raw)
	}

	job := rivertest.RequireInserted(t.Context(), t, riverpgxv5.New(pool), &SendEmailArgs{}, &rivertest.RequireInsertedOpts{Queue: QueueEmail, MaxAttempts: 5})
	if _, err := box.Decrypt(job.Args.Message, emailAAD); err != nil {
		t.Errorf("the queued email cannot be decrypted: %v", err)
	}

	// An invalid message is rejected when queuing, not in the worker.
	if err := NewMailSender(client, box).Send(t.Context(), mail.Message{To: "no es un email"}); err == nil {
		t.Error("want a validation error")
	}
}

// workEmail queues the encrypted message and processes it with the worker, as River would.
// It returns the worker's error, if any.
func workEmail(t *testing.T, sender mail.Sender, message string) (*rivertest.WorkResult, error) {
	t.Helper()
	pool := testutil.NewDB(t)
	box := testBox(t)
	worker := rivertest.NewWorker(t, riverpgxv5.New(pool), &river.Config{}, &sendEmailWorker{sender: sender, box: box})

	if message == "" {
		plain := `{"To":"alice@example.com","Subject":"Reset your password","Body":"hello"}`
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
	if msg, ok := recorder.Last("alice@example.com"); !ok || msg.Subject != "Reset your password" {
		t.Errorf("the email was not sent: %+v", recorder.Messages())
	}
}

type failingSender struct{}

func (failingSender) Send(context.Context, mail.Message) error {
	return errors.New("SMTP server down")
}

func TestSendEmailWorkerRetriesOnFailure(t *testing.T) {
	// If SMTP fails, the job stays to be retried later.
	res, err := workEmail(t, failingSender{}, "")
	if err == nil {
		t.Fatal("want the SMTP error")
	}
	// It stays retryable, or available if the first retry is almost immediate: in both cases River
	// tries it again. What must not happen is that it ends up discarded or completed.
	retried := res.Job.State == rivertype.JobStateRetryable || res.Job.State == rivertype.JobStateAvailable
	if !retried || len(res.Job.Errors) != 1 || res.Job.Attempt >= res.Job.MaxAttempts {
		t.Errorf("the email should be retried: state %s, attempt %d of %d, errors %d",
			res.Job.State, res.Job.Attempt, res.Job.MaxAttempts, len(res.Job.Errors))
	}
}

func TestSendEmailWorkerCancelsUndecryptable(t *testing.T) {
	// An email that cannot be decrypted is not retried: it would be useless.
	res, _ := workEmail(t, &mail.Recorder{}, "00000000:AAAA")
	if res.Job.State != rivertype.JobStateCancelled {
		t.Errorf("state = %s, want cancelled", res.Job.State)
	}
}

// QueuedEmailKeys counts the emails not sent yet, by the key they were encrypted with.
func TestQueuedEmailKeys(t *testing.T) {
	pool := testutil.NewDB(t)
	boxA := testBox(t)
	boxB, err := secretbox.New(bytes.Repeat([]byte{2}, 32), testutil.EncryptionKey) // B current, A previous
	if err != nil {
		t.Fatal(err)
	}
	client, err := New(pool, slog.New(slog.DiscardHandler), Config{MaxWorkers: 1}, Deps{Queries: store.New(pool), Mailer: &mail.Recorder{}, Box: boxB})
	if err != nil {
		t.Fatal(err)
	}

	// One with each key. The client did not start, so they stay waiting.
	for _, box := range []*secretbox.Box{boxA, boxB} {
		if err := NewMailSender(client, box).Send(t.Context(), resetEmail); err != nil {
			t.Fatal(err)
		}
	}
	// A cancelled one is no longer in the queue: it does not count.
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
		t.Errorf("QueuedEmailKeys = %v, want %v", got, want)
	}
}
