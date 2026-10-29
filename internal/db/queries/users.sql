-- name: CreateUser :one
INSERT INTO users (name, email, password_hash, role, email_verified_at, locale)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING id, name, email, password_hash, role, email_verified_at, totp_secret, totp_enabled_at, totp_last_step, locale, created_at, updated_at;

-- name: GetUserByEmail :one
SELECT id, name, email, password_hash, role, email_verified_at, totp_secret, totp_enabled_at, totp_last_step, locale, created_at, updated_at
FROM users
WHERE email = $1;

-- name: GetUserByID :one
SELECT id, name, email, password_hash, role, email_verified_at, totp_secret, totp_enabled_at, totp_last_step, locale, created_at, updated_at
FROM users
WHERE id = $1;

-- UpdateUserProfile changes the name and, if given, the language (a NULL locale leaves it as it is).
-- name: UpdateUserProfile :one
UPDATE users
SET name = sqlc.arg('name'), locale = coalesce(sqlc.narg('locale'), locale), updated_at = now()
WHERE id = sqlc.arg('id')
RETURNING id, name, email, password_hash, role, email_verified_at, totp_secret, totp_enabled_at, totp_last_step, locale, created_at, updated_at;

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
RETURNING id, name, email, password_hash, role, email_verified_at, totp_secret, totp_enabled_at, totp_last_step, locale, created_at, updated_at;

-- LockAdmins returns the admins and locks their rows until the end of the transaction: that way two
-- admins deleting their accounts at once cannot leave the API without any.
-- name: LockAdmins :many
SELECT id FROM users
WHERE role = 'admin'
ORDER BY id
FOR UPDATE;

-- DeleteUser deletes the user. Their sessions, tokens, notes, notifications and recovery
-- codes are deleted on their own (ON DELETE CASCADE).
-- name: DeleteUser :execrows
DELETE FROM users
WHERE id = $1;
