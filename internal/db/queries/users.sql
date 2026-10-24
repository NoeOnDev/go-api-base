-- name: CreateUser :one
INSERT INTO users (name, email, password_hash, role, email_verified_at)
VALUES ($1, $2, $3, $4, $5)
RETURNING id, name, email, password_hash, role, email_verified_at, totp_secret, totp_enabled_at, totp_last_step, created_at, updated_at;

-- name: GetUserByEmail :one
SELECT id, name, email, password_hash, role, email_verified_at, totp_secret, totp_enabled_at, totp_last_step, created_at, updated_at
FROM users
WHERE email = $1;

-- name: GetUserByID :one
SELECT id, name, email, password_hash, role, email_verified_at, totp_secret, totp_enabled_at, totp_last_step, created_at, updated_at
FROM users
WHERE id = $1;

-- name: UpdateUserName :one
UPDATE users
SET name = $2, updated_at = now()
WHERE id = $1
RETURNING id, name, email, password_hash, role, email_verified_at, totp_secret, totp_enabled_at, totp_last_step, created_at, updated_at;

-- name: UpdateUserPassword :exec
UPDATE users
SET password_hash = $2, updated_at = now()
WHERE id = $1;

-- name: MarkEmailVerified :exec
UPDATE users
SET email_verified_at = coalesce(email_verified_at, now()), updated_at = now()
WHERE id = $1;

-- name: SetPendingTOTPSecret :execrows
UPDATE users
SET totp_secret = $2, totp_last_step = NULL, updated_at = now()
WHERE id = $1 AND totp_enabled_at IS NULL;

-- name: EnableTOTP :execrows
UPDATE users
SET totp_enabled_at = now(), totp_last_step = $2, updated_at = now()
WHERE id = $1 AND totp_secret IS NOT NULL AND totp_enabled_at IS NULL;

-- name: DisableTOTP :exec
UPDATE users
SET totp_secret = NULL, totp_enabled_at = NULL, totp_last_step = NULL, updated_at = now()
WHERE id = $1;

-- UseTOTPStep registra el paso del código usado solo si es posterior al último: si devuelve
-- 0 filas, el código ya se había usado y se rechaza. Es atómico ante peticiones simultáneas.
-- name: UseTOTPStep :execrows
UPDATE users
SET totp_last_step = $2
WHERE id = $1 AND (totp_last_step IS NULL OR totp_last_step < $2);
