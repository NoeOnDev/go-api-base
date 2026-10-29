package jobs

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivertype"

	"github.com/NoeOnDev/go-api-base/internal/mail"
	"github.com/NoeOnDev/go-api-base/internal/secretbox"
)

// SendEmailArgs is the job of sending an email. The whole message (recipient, subject and body)
// is encrypted: reset emails carry a link with a token, and unencrypted, whoever reads the
// river_job table could use a valid link.
type SendEmailArgs struct {
	Message string `json:"message"` // mail.Message in JSON, encrypted with secretbox
}

func (SendEmailArgs) Kind() string { return "send_email" }

// InsertOpts sends the email to its queue, with up to 5 attempts. Between attempts River waits
// longer each time (seconds, then minutes), so a short SMTP outage does not lose emails.
func (SendEmailArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: QueueEmail, MaxAttempts: 5}
}

// emailAAD binds the encrypted message to its use: it cannot be decrypted as another kind of data.
var emailAAD = []byte("jobs.send_email")

// MailSender is the production mail.Sender: instead of sending, it queues the email. That way
// the HTTP response does not wait for the mail server, and if it fails or the API restarts, the
// email is not lost.
type MailSender struct {
	client *Client
	box    *secretbox.Box
}

// NewMailSender creates the sender that queues in client, encrypting with box.
func NewMailSender(client *Client, box *secretbox.Box) *MailSender {
	return &MailSender{client: client, box: box}
}

func (s *MailSender) Send(ctx context.Context, msg mail.Message) error {
	if err := msg.Validate(); err != nil {
		return err
	}
	plain, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("encode the email: %w", err)
	}
	encrypted, err := s.box.Encrypt(plain, emailAAD)
	if err != nil {
		return fmt.Errorf("encrypt the email: %w", err)
	}
	if _, err := s.client.Insert(ctx, SendEmailArgs{Message: encrypted}, nil); err != nil {
		return fmt.Errorf("queue the email: %w", err)
	}
	return nil
}

// sendEmailWorker sends the queued emails with the real sender (SMTP or log).
type sendEmailWorker struct {
	river.WorkerDefaults[SendEmailArgs]
	sender mail.Sender
	box    *secretbox.Box
}

func (w *sendEmailWorker) Work(ctx context.Context, job *river.Job[SendEmailArgs]) error {
	plain, err := w.box.Decrypt(job.Args.Message, emailAAD)
	if err != nil {
		// Retrying is useless: the key is missing or the data is damaged.
		return river.JobCancel(fmt.Errorf("decrypt the email: %w", err))
	}
	var msg mail.Message
	if err := json.Unmarshal(plain, &msg); err != nil {
		return river.JobCancel(fmt.Errorf("decode the email: %w", err))
	}
	return w.sender.Send(ctx, msg)
}

// QueuedEmailKeys counts the emails not sent yet (waiting, scheduled, retrying or being sent),
// by the ID of the key they were encrypted with. rotate-keys uses it: an email encrypted with a
// previous key is cancelled if the key is removed before it goes out.
func QueuedEmailKeys(ctx context.Context, pool *pgxpool.Pool) (map[string]int64, error) {
	// Without queues or workers, the client only reads and queues: it runs nothing.
	client, err := river.NewClient(riverpgxv5.New(pool), &river.Config{})
	if err != nil {
		return nil, fmt.Errorf("create the River client: %w", err)
	}

	const pageSize = 1000
	params := river.NewJobListParams().
		Kinds(SendEmailArgs{}.Kind()).
		States(rivertype.JobStateAvailable, rivertype.JobStatePending, rivertype.JobStateRetryable,
			rivertype.JobStateRunning, rivertype.JobStateScheduled).
		First(pageSize)

	counts := map[string]int64{}
	for {
		res, err := client.JobList(ctx, params)
		if err != nil {
			return nil, fmt.Errorf("list the queued emails: %w", err)
		}
		for _, job := range res.Jobs {
			var args SendEmailArgs
			if err := json.Unmarshal(job.EncodedArgs, &args); err != nil {
				return nil, fmt.Errorf("read queued email %d: %w", job.ID, err)
			}
			counts[secretbox.KeyIDOf(args.Message)]++
		}
		if len(res.Jobs) < pageSize {
			return counts, nil
		}
		params = params.After(res.LastCursor)
	}
}
