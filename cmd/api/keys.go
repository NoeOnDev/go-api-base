package main

import (
	"fmt"

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
