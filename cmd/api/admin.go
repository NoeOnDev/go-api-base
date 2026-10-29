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

// runCreateAdmin crea un usuario con rol admin:
//
//	api create-admin -email admin@ejemplo.com -name "Administrador"
//
// La contraseña se toma de ADMIN_PASSWORD o se pide por la terminal sin mostrarla.
func runCreateAdmin(args []string) error {
	fs := flag.NewFlagSet("create-admin", flag.ContinueOnError)
	email := fs.String("email", "", "email del administrador (obligatorio)")
	name := fs.String("name", "Administrador", "nombre del administrador")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *email == "" {
		fs.Usage()
		return errors.New("falta -email")
	}

	password, err := adminPassword()
	if err != nil {
		return err
	}

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("configuración: %w", err)
	}

	ctx := context.Background()
	pool, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	// Se migra primero para poder crear el admin antes del primer arranque del servidor.
	if err := db.Migrate(ctx, pool, slog.New(slog.DiscardHandler)); err != nil {
		return err
	}

	service := auth.NewService(pool, auth.Options{})
	u, err := service.CreateUser(ctx, auth.RegisterInput{Name: *name, Email: *email, Password: password}, auth.RoleAdmin)
	if err != nil {
		return describe(err)
	}

	fmt.Printf("Administrador creado: %s (id %d)\n", u.Email, u.ID)
	return nil
}

// adminPassword lee la contraseña de ADMIN_PASSWORD o la pide dos veces por la terminal.
func adminPassword() (string, error) {
	if p := os.Getenv("ADMIN_PASSWORD"); p != "" {
		return p, nil
	}

	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return "", errors.New("definí ADMIN_PASSWORD o ejecutá el comando en una terminal interactiva")
	}

	fmt.Print("Contraseña: ")
	first, err := term.ReadPassword(fd)
	fmt.Println()
	if err != nil {
		return "", fmt.Errorf("leer contraseña: %w", err)
	}

	fmt.Print("Repetí la contraseña: ")
	second, err := term.ReadPassword(fd)
	fmt.Println()
	if err != nil {
		return "", fmt.Errorf("leer contraseña: %w", err)
	}

	if string(first) != string(second) {
		return "", errors.New("las contraseñas no coinciden")
	}
	return string(first), nil
}

// describe convierte los errores de validación de la API en un mensaje legible para la terminal.
func describe(err error) error {
	var apiErr *httpx.Error
	if !errors.As(err, &apiErr) {
		return err
	}
	// La terminal es del operador: los mensajes van en inglés, el idioma del proyecto.
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
