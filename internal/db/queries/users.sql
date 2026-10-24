-- name: CreateUser :one
INSERT INTO users (name, email, password_hash, role)
VALUES ($1, $2, $3, $4)
RETURNING id, name, email, password_hash, role, created_at, updated_at;

-- name: GetUserByEmail :one
SELECT id, name, email, password_hash, role, created_at, updated_at
FROM users
WHERE email = $1;
