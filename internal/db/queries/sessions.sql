-- name: CreateSession :exec
INSERT INTO sessions (token_hash, user_id, expires_at)
VALUES ($1, $2, $3);

-- name: GetSessionUser :one
SELECT u.id, u.name, u.email, u.password_hash, u.role, u.email_verified_at, u.created_at, u.updated_at
FROM sessions s
JOIN users u ON u.id = s.user_id
WHERE s.token_hash = $1 AND s.expires_at > now();

-- name: DeleteSession :exec
DELETE FROM sessions
WHERE token_hash = $1;

-- name: DeleteExpiredSessions :execrows
DELETE FROM sessions
WHERE expires_at <= now();

-- name: DeleteUserSessions :exec
DELETE FROM sessions
WHERE user_id = $1;

-- name: DeleteOtherSessions :exec
DELETE FROM sessions
WHERE user_id = $1 AND token_hash <> $2;
