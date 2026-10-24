// Package migrations contiene las migraciones SQL de la base de datos.
// Se embeben en el binario para que la API pueda aplicarlas al arrancar,
// sin depender de que los archivos .sql estén en el servidor.
//
// Cada archivo usa el formato de goose: un número de versión creciente
// y secciones "-- +goose Up" y "-- +goose Down". Para crear una nueva:
//
//	make migrate-new name=create_products
package migrations

import "embed"

// FS contiene todos los archivos .sql de esta carpeta.
//
//go:embed *.sql
var FS embed.FS
