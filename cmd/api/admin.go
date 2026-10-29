package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
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
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *email == "" {
		fs.Usage()
		return errors.New("missing -email")
	}

	password, err := adminPassword()
	if err != nil {
		return err
	}

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
	return nil
}

// adminPassword reads the password from ADMIN_PASSWORD or asks for it twice in the terminal.
func adminPassword() (string, error) {
	if p := os.Getenv("ADMIN_PASSWORD"); p != "" {
		return p, nil
	}

	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return "", errors.New("set ADMIN_PASSWORD or run the command in an interactive terminal")
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
