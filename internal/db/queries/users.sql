-- name: CreateUser :one
INSERT INTO users (name, email, password_hash, role, email_verified_at)
VALUES ($1, $2, $3, $4, $5)
RETURNING id, name, email, password_hash, role, email_verified_at, created_at, updated_at;

-- name: GetUserByEmail :one
SELECT id, name, email, password_hash, role, email_verified_at, created_at, updated_at
FROM users
WHERE email = $1;

-- name: GetUserByID :one
SELECT id, name, email, password_hash, role, email_verified_at, created_at, updated_at
FROM users
WHERE id = $1;

-- name: UpdateUserName :one
UPDATE users
SET name = $2, updated_at = now()
WHERE id = $1
RETURNING id, name, email, password_hash, role, email_verified_at, created_at, updated_at;

-- name: UpdateUserPassword :exec
UPDATE users
SET password_hash = $2, updated_at = now()
WHERE id = $1;

-- name: MarkEmailVerified :exec
UPDATE users
SET email_verified_at = coalesce(email_verified_at, now()), updated_at = now()
WHERE id = $1;
