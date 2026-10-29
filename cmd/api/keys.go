package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/NoeOnDev/go-api-base/internal/auth"
	"github.com/NoeOnDev/go-api-base/internal/config"
	"github.com/NoeOnDev/go-api-base/internal/db"
	"github.com/NoeOnDev/go-api-base/internal/jobs"
	"github.com/NoeOnDev/go-api-base/internal/secretbox"
)

// runGenerateKey prints a new key for ENCRYPTION_KEY (like `php artisan key:generate`, but it
// does not modify any file). It needs neither .env nor a database.
func runGenerateKey() error {
	key, err := secretbox.GenerateKey()
	if err != nil {
		return err
	}
	fmt.Println(key)
	return nil
}

// runRotateKeys encrypts again with the current key everything that is still under previous
// keys and reports what still depends on them. It ends with an error if something still depends
// on an old key: in that case, it must not be removed from ENCRYPTION_PREVIOUS_KEYS yet.
func runRotateKeys() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("configuration: %w", err)
	}

	ctx := context.Background()
	pool, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := db.Migrate(ctx, pool, slog.New(slog.DiscardHandler)); err != nil {
		return err
	}

	box, err := secretbox.New(cfg.EncryptionKey, cfg.EncryptionPreviousKeys...)
	if err != nil {
		return err
	}
	report, err := auth.NewService(pool, auth.Options{Box: box}).RotateKeys(ctx)
	if err != nil {
		return err
	}
	queued, err := jobs.QueuedEmailKeys(ctx, pool)
	if err != nil {
		return err
	}
	report.AddQueuedEmails(queued)

	fmt.Printf("Current key: %s\n", report.CurrentKeyID)
	fmt.Printf("2FA secrets encrypted again: %d. Already with the current key: %d.\n",
		report.Reencrypted, report.AlreadyCurrent)

	if len(report.Undecryptable) > 0 {
		fmt.Printf("\n2FA secrets that could not be decrypted (%d):\n", len(report.Undecryptable))
		for _, u := range report.Undecryptable {
			fmt.Printf("  - user %d (key %q): %s\n", u.UserID, u.KeyID, u.Reason)
		}
		fmt.Println("  Add its key to ENCRYPTION_PREVIOUS_KEYS and run the command again, or turn off their 2FA (see docs/encryption.md).")
	}

	if len(report.OldRecoveryCodes) > 0 {
		fmt.Println("\nRecovery codes computed with previous keys:")
		for _, k := range report.OldRecoveryCodes {
			fmt.Printf("  - key %s: %s\n", k.KeyID, users(k.Users))
		}
		fmt.Println("  They cannot be recomputed: those users have to regenerate them from their profile.")
		fmt.Println("  If you remove the key, their current codes stop working (their authenticator app keeps working).")
	}

	if len(report.QueuedEmails) > 0 {
		fmt.Println("\nQueued emails encrypted with previous keys (not sent yet):")
		for _, k := range report.QueuedEmails {
			fmt.Printf("  - key %s: %s\n", k.KeyID, emails(k.Count))
		}
		fmt.Println("  They go out on their own in seconds or minutes (if SMTP fails, they are retried); if you remove the key before, they are cancelled.")
		fmt.Println("  Wait until they go out and run the command again.")
	}

	if len(report.RemovableKeys) > 0 {
		fmt.Printf("\nPrevious keys no longer in use that can be removed from ENCRYPTION_PREVIOUS_KEYS: %v\n", report.RemovableKeys)
	}

	if !report.Done() {
		return errors.New("there is still data that depends on previous keys: do not remove them from ENCRYPTION_PREVIOUS_KEYS")
	}
	fmt.Println("\nDone: nothing depends on previous keys.")
	return nil
}

func users(n int64) string {
	if n == 1 {
		return "1 user"
	}
	return fmt.Sprintf("%d users", n)
}

func emails(n int64) string {
	if n == 1 {
		return "1 email"
	}
	return fmt.Sprintf("%d emails", n)
}
