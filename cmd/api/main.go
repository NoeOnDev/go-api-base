// Command api starts the application's HTTP server.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
	// The time zone database, inside the binary: the zones of the users and of APP_TIME_ZONE work
	// even in a container without /usr/share/zoneinfo. If the system has it, Go uses the system's,
	// which may be more up to date (see "Dates in UTC" in docs/decisions.md).
	_ "time/tzdata"

	"github.com/NoeOnDev/go-api-base/internal/config"
	"github.com/NoeOnDev/go-api-base/internal/db"
	"github.com/NoeOnDev/go-api-base/internal/i18n"
	"github.com/NoeOnDev/go-api-base/internal/jobs"
	"github.com/NoeOnDev/go-api-base/internal/mail"
	"github.com/NoeOnDev/go-api-base/internal/realtime"
	"github.com/NoeOnDev/go-api-base/internal/secretbox"
	"github.com/NoeOnDev/go-api-base/internal/server"
	"github.com/NoeOnDev/go-api-base/internal/store"
)

func main() {
	var err error

	// Without arguments it starts the server. The subcommands are administration tasks that use the
	// same configuration and the same binary.
	switch {
	case len(os.Args) < 2:
		err = run()
	case os.Args[1] == "create-admin":
		err = runCreateAdmin(os.Args[2:])
	case os.Args[1] == "generate-key":
		err = runGenerateKey()
	case os.Args[1] == "rotate-keys":
		err = runRotateKeys()
	default:
		err = fmt.Errorf("unknown command %q (available: create-admin, generate-key, rotate-keys)", os.Args[1])
	}

	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

// run holds the real startup. Returning errors instead of calling os.Exit lets the defers run and
// makes the startup easier to test.
func run() error {
	// ctx is cancelled on Ctrl+C (SIGINT) or on the shutdown signal of systemd/Docker (SIGTERM).
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("configuration: %w", err)
	}

	logger := newLogger(cfg)
	slog.SetDefault(logger)
	for _, w := range cfg.Warnings() {
		logger.Warn(w)
	}

	pool, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	if err := db.Migrate(ctx, pool, logger); err != nil {
		return err
	}

	// config.Load already validated the keys.
	box, err := secretbox.New(cfg.EncryptionKey, cfg.EncryptionPreviousKeys...)
	if err != nil {
		return fmt.Errorf("ENCRYPTION_KEY: %w", err)
	}

	// config.Load already validated DEFAULT_LOCALE.
	catalog, err := i18n.New(cfg.DefaultLocale)
	if err != nil {
		return fmt.Errorf("DEFAULT_LOCALE: %w", err)
	}

	sender, err := newMailSender(cfg, logger)
	if err != nil {
		return err
	}

	// Background jobs: the workers run in this same process.
	// They start without the signals' ctx: that way the signal does not cut them abruptly, and
	// shutdown stops them with Stop and its own deadline, letting what is running finish.
	jobsClient, err := jobs.New(pool, logger, jobs.Config{MaxWorkers: cfg.JobsMaxWorkers}, jobs.Deps{
		Queries: store.New(pool),
		Logger:  logger,
		Mailer:  sender,
		Box:     box,
		// So the cleanup also deletes the idle sessions.
		SessionIdleTimeout: cfg.SessionIdleTimeout,
		TimeZone:           cfg.TimeZone,
	})
	if err != nil {
		return err
	}
	if err := jobsClient.Start(context.WithoutCancel(ctx)); err != nil {
		return fmt.Errorf("start the background jobs: %w", err)
	}

	// The app does not send the emails: it queues them, and a worker sends them (with retries).
	mailer := jobs.NewMailSender(jobsClient, box)

	// With LISTEN/NOTIFY, the real-time events and the session closings reach every instance of
	// the API. It keeps listening until the shutdown signal.
	broker := realtime.NewBroker()
	if err := broker.Listen(ctx, pool, logger); err != nil {
		return err
	}

	srv := server.New(cfg.HTTPAddr, server.NewHandler(server.Deps{
		Logger: logger,
		DB:     pool,
		Config: cfg,
		Mailer: mailer,
		Broker: broker,
		Jobs:   jobsClient,
		Box:    box,
		I18n:   catalog,
	}), logger)
	// On shutdown the SSE streams are closed; otherwise, Shutdown would wait until the timeout.
	srv.RegisterOnShutdown(broker.Close)

	// The server runs in another goroutine so this one can wait for the shutdown signal.
	serverErr := make(chan error, 1)
	go func() {
		logger.Info("server listening", "addr", cfg.HTTPAddr, "env", cfg.Env)
		serverErr <- srv.ListenAndServe()
	}()

	select {
	case err := <-serverErr:
		// ListenAndServe only returns before the shutdown if it could not start (for example, port in use).
		return fmt.Errorf("server: %w", err)
	case <-ctx.Done():
	}

	// Graceful shutdown: stop accepting connections and wait for the running requests and the jobs
	// being executed to finish.
	logger.Info("shutting down server", "timeout", cfg.ShutdownTimeout.String())
	shutdown(cfg.ShutdownTimeout, srv, jobsClient, logger)
	if err := <-serverErr; !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("server: %w", err)
	}

	logger.Info("server stopped")
	return nil
}

// stopper is what shutdown needs from the background jobs (the River client).
type stopper interface {
	Stop(ctx context.Context) error
}

// shutdown stops the HTTP server and the background jobs at the same time, each with the full
// timeout. That way a stuck request does not take time away from the jobs to stop in order: one
// cut halfway River retries only an hour later.
//
// Stopping them at once is safe: a request that is still finishing can queue a job after River
// stopped processing; it stays in the database and runs on the next start.
//
// It returns no error: what did not finish in time is left in the log.
func shutdown(timeout time.Duration, srv *http.Server, jobs stopper, logger *slog.Logger) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	jobsDone := make(chan error, 1)
	go func() { jobsDone <- jobs.Stop(ctx) }()

	if err := srv.Shutdown(ctx); err != nil {
		// The remaining connections (for example, a stuck one) are cut, instead of being left halfway.
		logger.Warn("requests still running at shutdown; cutting them", "error", err)
		_ = srv.Close()
	}
	if err := <-jobsDone; err != nil {
		logger.Warn("jobs left halfway at shutdown; River will retry them in an hour", "error", err)
	}
}

// newMailSender creates the email sender according to MAIL_DRIVER.
func newMailSender(cfg config.Config, logger *slog.Logger) (mail.Sender, error) {
	if cfg.Mail.Driver == config.MailDriverSMTP {
		s, err := mail.NewSMTPSender(cfg.Mail.SMTPHost, cfg.Mail.SMTPPort, cfg.Mail.SMTPUser, cfg.Mail.SMTPPass, cfg.Mail.From)
		if err != nil {
			return nil, fmt.Errorf("mail configuration: %w", err)
		}
		return s, nil
	}
	return mail.LogSender{Logger: logger}, nil
}

// newLogger returns a logger in readable text for development and in JSON for production,
// which is the format that log processing tools expect.
func newLogger(cfg config.Config) *slog.Logger {
	opts := &slog.HandlerOptions{Level: cfg.LogLevel}
	if cfg.IsProduction() {
		return slog.New(slog.NewJSONHandler(os.Stdout, opts))
	}
	return slog.New(slog.NewTextHandler(os.Stdout, opts))
}
