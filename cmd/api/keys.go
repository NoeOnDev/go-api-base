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

// runGenerateKey imprime una clave nueva para ENCRYPTION_KEY (equivale a `php artisan key:generate`,
// pero no modifica ningún archivo). No necesita .env ni base de datos.
func runGenerateKey() error {
	key, err := secretbox.GenerateKey()
	if err != nil {
		return err
	}
	fmt.Println(key)
	return nil
}

// runRotateKeys vuelve a cifrar con la clave actual todo lo que esté con claves anteriores e
// informa lo que todavía depende de ellas. Termina con error si algo sigue dependiendo de una
// clave vieja: en ese caso, todavía no hay que quitarla de ENCRYPTION_PREVIOUS_KEYS.
func runRotateKeys() error {
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

	fmt.Printf("Clave actual: %s\n", report.CurrentKeyID)
	fmt.Printf("Secretos del 2FA vueltos a cifrar: %d. Ya con la clave actual: %d.\n",
		report.Reencrypted, report.AlreadyCurrent)

	if len(report.Undecryptable) > 0 {
		fmt.Printf("\nSecretos del 2FA que no se pudieron descifrar (%d):\n", len(report.Undecryptable))
		for _, u := range report.Undecryptable {
			fmt.Printf("  - usuario %d (clave %q): %s\n", u.UserID, u.KeyID, u.Reason)
		}
		fmt.Println("  Agregá su clave a ENCRYPTION_PREVIOUS_KEYS y volvé a correr el comando, o desactivales el 2FA (ver docs/encryption.md).")
	}

	if len(report.OldRecoveryCodes) > 0 {
		fmt.Println("\nCódigos de recuperación calculados con claves anteriores:")
		for _, k := range report.OldRecoveryCodes {
			fmt.Printf("  - clave %s: %s\n", k.KeyID, users(k.Users))
		}
		fmt.Println("  No se pueden recalcular: esos usuarios tienen que regenerarlos desde su perfil.")
		fmt.Println("  Si quitás la clave, sus códigos actuales dejan de servir (su app de autenticación sí sigue funcionando).")
	}

	if len(report.QueuedEmails) > 0 {
		fmt.Println("\nCorreos en la cola cifrados con claves anteriores (todavía no salieron):")
		for _, k := range report.QueuedEmails {
			fmt.Printf("  - clave %s: %s\n", k.KeyID, emails(k.Count))
		}
		fmt.Println("  Salen solos en segundos o minutos (si el SMTP falla, se reintentan); si quitás la clave antes, se cancelan.")
		fmt.Println("  Esperá a que salgan y volvé a correr el comando.")
	}

	if len(report.RemovableKeys) > 0 {
		fmt.Printf("\nClaves anteriores que ya no se usan y se pueden quitar de ENCRYPTION_PREVIOUS_KEYS: %v\n", report.RemovableKeys)
	}

	if !report.Done() {
		return errors.New("todavía hay datos que dependen de claves anteriores: no las quites de ENCRYPTION_PREVIOUS_KEYS")
	}
	fmt.Println("\nListo: nada depende de claves anteriores.")
	return nil
}

func users(n int64) string {
	if n == 1 {
		return "1 usuario"
	}
	return fmt.Sprintf("%d usuarios", n)
}

func emails(n int64) string {
	if n == 1 {
		return "1 correo"
	}
	return fmt.Sprintf("%d correos", n)
}
