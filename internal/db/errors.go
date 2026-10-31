package db

import (
	"errors"
	"slices"

	"github.com/jackc/pgx/v5/pgconn"
)

// PostgreSQL error codes that the API turns into responses for the client.
// Full list: https://www.postgresql.org/docs/current/errcodes-appendix.html
const (
	codeUniqueViolation     = "23505"
	codeForeignKeyViolation = "23503"
	// codeRestrictViolation is what a FK with ON DELETE RESTRICT returns when deleting a row that
	// is still in use (FKs without that option return codeForeignKeyViolation).
	codeRestrictViolation = "23001"
	// codeExclusionViolation is an EXCLUDE constraint: for example, two appointments of the same
	// branch that overlap (see migrations/00013_create_appointments.sql).
	codeExclusionViolation = "23P01"
)

// IsUniqueViolation reports whether err is a UNIQUE violation (for example, a repeated email)
// and returns the name of the constraint, to know which field to answer.
func IsUniqueViolation(err error) (constraint string, ok bool) {
	return pgErrorConstraint(err, codeUniqueViolation)
}

// IsForeignKeyViolation reports whether err is a FOREIGN KEY violation: a row that does not
// exist was referenced, or a row that others still reference was deleted.
func IsForeignKeyViolation(err error) (constraint string, ok bool) {
	return pgErrorConstraint(err, codeForeignKeyViolation, codeRestrictViolation)
}

// IsExclusionViolation reports whether err is an EXCLUDE violation: a row that overlaps another
// (two bookings of the same slot) and returns the name of the constraint.
func IsExclusionViolation(err error) (constraint string, ok bool) {
	return pgErrorConstraint(err, codeExclusionViolation)
}

func pgErrorConstraint(err error, codes ...string) (string, bool) {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && slices.Contains(codes, pgErr.Code) {
		return pgErr.ConstraintName, true
	}
	return "", false
}
