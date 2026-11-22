package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"sort"

	"golang.org/x/term"

	"github.com/NoeOnDev/go-api-base/internal/auth"
	"github.com/NoeOnDev/go-api-base/internal/config"
	"github.com/NoeOnDev/go-api-base/internal/db"
	"github.com/NoeOnDev/go-api-base/internal/httpx"
	"github.com/NoeOnDev/go-api-base/internal/i18n"
)

// runCreateAdmin creates a user with the admin role:
//
//	api create-admin -email admin@example.com -name "Administrator"
//
// The password is taken from ADMIN_PASSWORD or asked for in the terminal without showing it.
func runCreateAdmin(args []string) error {
	fs := flag.NewFlagSet("create-admin", flag.ContinueOnError)
	email := fs.String("email", "", "administrator's email (required)")
	name := fs.String("name", "Administrator", "administrator's name")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if *email == "" {
		fs.Usage()
		return errors.New("missing -email")
	}

	// The configuration first: a mistake in it is said before asking for the password.
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("configuration: %w", err)
	}
	password, err := readPassword("ADMIN_PASSWORD")
	if err != nil {
		return err
	}

	ctx := context.Background()
	pool, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	// Migrate first, so the admin can be created before the server's first start.
	if err := db.Migrate(ctx, pool, slog.New(slog.DiscardHandler)); err != nil {
		return err
	}

	service := auth.NewService(pool, auth.Options{})
	u, err := service.CreateUser(ctx, auth.RegisterInput{Name: *name, Email: *email, Password: password}, auth.RoleAdmin)
	if err != nil {
		return describe(err)
	}

	fmt.Printf("Administrator created: %s (id %d)\n", u.Email, u.ID)
	if cfg.RequireAdminTwoFactor {
		fmt.Println("REQUIRE_ADMIN_TWO_FACTOR is on: log in and turn on two-factor authentication to use the admin routes.")
	}
	return nil
}

// readPassword reads the password from the environment variable (ADMIN_PASSWORD, SEED_PASSWORD) or
// asks for it twice in the terminal.
func readPassword(variable string) (string, error) {
	// Without the quotes a .env may have around the value: make keeps them, and they would be part
	// of the password.
	if p := config.Env(variable); p != "" {
		return p, nil
	}

	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return "", fmt.Errorf("set %s or run the command in an interactive terminal", variable)
	}
	// The terminal does not show what is typed. Ctrl+C in the middle must leave it as it was, or
	// the shell would stay without showing what the person types afterwards.
	if state, err := term.GetState(fd); err == nil {
		interrupted := make(chan os.Signal, 1)
		signal.Notify(interrupted, os.Interrupt)
		defer signal.Stop(interrupted)
		go func() {
			if _, ok := <-interrupted; ok {
				_ = term.Restore(fd, state)
				fmt.Println()
				os.Exit(130)
			}
		}()
		defer close(interrupted)
	}

	fmt.Print("Password: ")
	first, err := term.ReadPassword(fd)
	fmt.Println()
	if err != nil {
		return "", fmt.Errorf("read password: %w", err)
	}

	fmt.Print("Repeat the password: ")
	second, err := term.ReadPassword(fd)
	fmt.Println()
	if err != nil {
		return "", fmt.Errorf("read password: %w", err)
	}

	if string(first) != string(second) {
		return "", errors.New("the passwords do not match")
	}
	return string(first), nil
}

// describe turns the API's validation errors into a message readable in the terminal.
func describe(err error) error {
	var apiErr *httpx.Error
	if !errors.As(err, &apiErr) {
		return err
	}
	// The terminal belongs to the operator: the messages go in English, the project's language.
	t := i18n.Default().Translator(i18n.BaseLocale)
	if apiErr.Code != "validation_failed" {
		return errors.New(apiErr.Message(t))
	}

	fields := apiErr.FieldMessages(t)
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	msg := apiErr.Message(t)
	for _, k := range keys {
		msg += fmt.Sprintf("\n  - %s: %s", k, fields[k])
	}
	return errors.New(msg)
}
