package db

import (
	"errors"
	"slices"

	"github.com/jackc/pgx/v5/pgconn"
)

// Códigos de error de PostgreSQL que la API traduce a respuestas para el cliente.
// Lista completa: https://www.postgresql.org/docs/current/errcodes-appendix.html
const (
	codeUniqueViolation     = "23505"
	codeForeignKeyViolation = "23503"
	// codeRestrictViolation es el que devuelve una FK con ON DELETE RESTRICT al borrar
	// una fila que todavía se usa (las FK sin esa opción devuelven codeForeignKeyViolation).
	codeRestrictViolation = "23001"
)

// IsUniqueViolation indica si err es una violación de UNIQUE (por ejemplo, un email repetido)
// y devuelve el nombre de la restricción, para saber qué campo responder.
func IsUniqueViolation(err error) (constraint string, ok bool) {
	return pgErrorConstraint(err, codeUniqueViolation)
}

// IsForeignKeyViolation indica si err es una violación de FOREIGN KEY: se referenció una fila
// que no existe, o se intentó borrar una fila que otras todavía referencian.
func IsForeignKeyViolation(err error) (constraint string, ok bool) {
	return pgErrorConstraint(err, codeForeignKeyViolation, codeRestrictViolation)
}

func pgErrorConstraint(err error, codes ...string) (string, bool) {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && slices.Contains(codes, pgErr.Code) {
		return pgErr.ConstraintName, true
	}
	return "", false
}
