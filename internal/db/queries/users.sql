-- name: CreateUser :one
INSERT INTO users (name, email, password_hash, role, email_verified_at, locale, time_zone)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING id, name, email, password_hash, role, email_verified_at, totp_secret, totp_enabled_at, totp_last_step, locale, time_zone, suspended_at, created_at, updated_at;

-- name: GetUserByEmail :one
SELECT id, name, email, password_hash, role, email_verified_at, totp_secret, totp_enabled_at, totp_last_step, locale, time_zone, suspended_at, created_at, updated_at
FROM users
WHERE email = $1;

-- name: GetUserByID :one
SELECT id, name, email, password_hash, role, email_verified_at, totp_secret, totp_enabled_at, totp_last_step, locale, time_zone, suspended_at, created_at, updated_at
FROM users
WHERE id = $1;

-- UpdateUserProfile changes the name and, if given, the language and the time zone (a NULL leaves
-- them as they are).
-- name: UpdateUserProfile :one
UPDATE users
SET name = sqlc.arg('name'),
    locale = coalesce(sqlc.narg('locale'), locale),
    time_zone = coalesce(sqlc.narg('time_zone'), time_zone),
    updated_at = now()
WHERE id = sqlc.arg('id')
RETURNING id, name, email, password_hash, role, email_verified_at, totp_secret, totp_enabled_at, totp_last_step, locale, time_zone, suspended_at, created_at, updated_at;

-- name: GetUserLocale :one
SELECT locale FROM users
WHERE id = $1;

-- name: UpdateUserPassword :exec
UPDATE users
SET password_hash = $2, updated_at = now()
WHERE id = $1;

-- RehashPassword stores the same password with a hash of new parameters. It only changes it if it
-- is still the one that was verified, so it does not overwrite a password change made at the same time.
-- It does not touch updated_at: for the user, nothing changed.
-- name: RehashPassword :execrows
UPDATE users
SET password_hash = sqlc.arg('new_hash')
WHERE id = sqlc.arg('id') AND password_hash = sqlc.arg('old_hash');

-- name: MarkEmailVerified :exec
UPDATE users
SET email_verified_at = coalesce(email_verified_at, now()), updated_at = now()
WHERE id = $1;

-- name: SetPendingTOTPSecret :execrows
UPDATE users
SET totp_secret = $2, totp_last_step = NULL, updated_at = now()
WHERE id = $1 AND totp_enabled_at IS NULL;

-- EnableTOTP turns on 2FA only if the secret is still the one that was verified: if meanwhile
-- another request asked for a new QR code (setup), it does not turn on one the user never scanned.
-- name: EnableTOTP :execrows
UPDATE users
SET totp_enabled_at = now(), totp_last_step = sqlc.arg('totp_last_step'), updated_at = now()
WHERE id = sqlc.arg('id') AND totp_secret = sqlc.arg('totp_secret') AND totp_enabled_at IS NULL;

-- name: DisableTOTP :exec
UPDATE users
SET totp_secret = NULL, totp_enabled_at = NULL, totp_last_step = NULL, updated_at = now()
WHERE id = $1;

-- UseTOTPStep records the step of the used code only if it is later than the last one: if it returns
-- 0 rows, the code was already used and is rejected. It is atomic against simultaneous requests.
-- name: UseTOTPStep :execrows
UPDATE users
SET totp_last_step = $2
WHERE id = $1 AND (totp_last_step IS NULL OR totp_last_step < $2);

-- UpdateTOTPSecret encrypts the secret again with another key. It only changes it if it is still
-- the one that was read, so it does not overwrite a change made at the same time (for example, turning off 2FA).
-- name: UpdateTOTPSecret :execrows
UPDATE users
SET totp_secret = sqlc.arg('new_secret')
WHERE id = sqlc.arg('id') AND totp_secret = sqlc.arg('old_secret');

-- name: ListUsersWithTOTPSecret :many
SELECT id, totp_secret
FROM users
WHERE totp_secret IS NOT NULL
ORDER BY id;

-- LockUser locks the user's row until the end of the transaction. It orders simultaneous
-- operations on the same user (for example, two requests for a reset link).
-- name: LockUser :exec
SELECT id FROM users
WHERE id = $1
FOR UPDATE;

-- UpdateUserEmail changes the email, already confirmed from the new address.
-- name: UpdateUserEmail :one
UPDATE users
SET email = $2, email_verified_at = now(), updated_at = now()
WHERE id = $1
RETURNING id, name, email, password_hash, role, email_verified_at, totp_secret, totp_enabled_at, totp_last_step, locale, time_zone, suspended_at, created_at, updated_at;

-- LockAdmins returns the active admins (not suspended, and with a password: an invitation not
-- accepted yet does not count) and locks their rows until the end of the transaction: that way two
-- admins deleting their accounts, or demoting each other, at once cannot leave the API without any.
-- name: LockAdmins :many
SELECT id FROM users
WHERE role = 'admin' AND suspended_at IS NULL AND password_hash IS NOT NULL
ORDER BY id
FOR UPDATE;

-- DeleteUser deletes the user. Their sessions, tokens, notes, notifications and recovery
-- codes are deleted on their own (ON DELETE CASCADE).
-- name: DeleteUser :execrows
DELETE FROM users
WHERE id = $1;

-- ListUsers is the administrators' list, with optional filters: search (in the name or the
-- email, already escaped for ILIKE), role and status (active, invited or suspended). The ILIKE '%…%'
-- search does not use indexes; if the table grows, see pg_trgm in docs/adding-a-resource.md.
-- name: ListUsers :many
SELECT id, name, email, password_hash, role, email_verified_at, totp_secret, totp_enabled_at, totp_last_step, locale, time_zone, suspended_at, created_at, updated_at
FROM users
WHERE (sqlc.narg('search')::text IS NULL OR name ILIKE '%' || sqlc.narg('search') || '%' OR email ILIKE '%' || sqlc.narg('search') || '%')
  AND (sqlc.narg('role')::text IS NULL OR role = sqlc.narg('role'))
  AND (sqlc.narg('status')::text IS NULL
       OR (sqlc.narg('status') = 'active' AND suspended_at IS NULL AND password_hash IS NOT NULL)
       OR (sqlc.narg('status') = 'invited' AND suspended_at IS NULL AND password_hash IS NULL)
       OR (sqlc.narg('status') = 'suspended' AND suspended_at IS NOT NULL))
ORDER BY id DESC
LIMIT sqlc.arg('limit') OFFSET sqlc.arg('offset');

-- name: CountUsers :one
SELECT count(*)
FROM users
WHERE (sqlc.narg('search')::text IS NULL OR name ILIKE '%' || sqlc.narg('search') || '%' OR email ILIKE '%' || sqlc.narg('search') || '%')
  AND (sqlc.narg('role')::text IS NULL OR role = sqlc.narg('role'))
  AND (sqlc.narg('status')::text IS NULL
       OR (sqlc.narg('status') = 'active' AND suspended_at IS NULL AND password_hash IS NOT NULL)
       OR (sqlc.narg('status') = 'invited' AND suspended_at IS NULL AND password_hash IS NULL)
       OR (sqlc.narg('status') = 'suspended' AND suspended_at IS NOT NULL));

-- name: UpdateUserRole :execrows
UPDATE users
SET role = $2, updated_at = now()
WHERE id = $1;

-- SuspendUser keeps the original date if the account was already suspended.
-- name: SuspendUser :execrows
UPDATE users
SET suspended_at = coalesce(suspended_at, now()), updated_at = now()
WHERE id = $1;

-- name: UnsuspendUser :execrows
UPDATE users
SET suspended_at = NULL, updated_at = now()
WHERE id = $1;

-- SetInitialPassword sets the password of an invited user, marks the email as verified (the link
-- reached it) and sets the language and the time zone if it has none. It only changes it if the
-- user still has no password: an invitation that was already accepted, or a password set with a
-- reset, cannot be overwritten with an old link.
-- name: SetInitialPassword :execrows
UPDATE users
SET password_hash = sqlc.arg('password_hash'),
    email_verified_at = coalesce(email_verified_at, now()),
    locale = coalesce(locale, sqlc.narg('locale')),
    time_zone = coalesce(time_zone, sqlc.narg('time_zone')),
    updated_at = now()
WHERE id = sqlc.arg('id') AND password_hash IS NULL;
