// Package migrations holds the SQL migrations of the database.
// They are embedded in the binary so the API can apply them on startup, without depending on
// the .sql files being on the server.
//
// Each file uses the goose format: an increasing version number and "-- +goose Up" and
// "-- +goose Down" sections. To create a new one:
//
//	make migrate-new name=create_products
package migrations

import "embed"

// FS holds every .sql file of this folder.
//
//go:embed *.sql
var FS embed.FS
