-- name: CreateSession :exec
INSERT INTO sessions (token_hash, user_id, expires_at, user_agent, ip)
VALUES ($1, $2, $3, $4, $5);

-- GetSessionUser busca la sesión vigente del token, con su usuario.
-- name: GetSessionUser :one
SELECT sqlc.embed(u), s.id AS session_id, s.last_seen_at
FROM sessions s
JOIN users u ON u.id = s.user_id
WHERE s.token_hash = $1 AND s.expires_at > now();

-- name: TouchSession :exec
UPDATE sessions
SET last_seen_at = now()
WHERE token_hash = $1;

-- name: ListUserSessions :many
SELECT id, user_agent, ip, created_at, last_seen_at
FROM sessions
WHERE user_id = $1 AND expires_at > now()
ORDER BY last_seen_at DESC, id DESC;

-- DeleteUserSession cierra una sesión del usuario, salvo la indicada (la de la petición).
-- Devuelve el hash del token, para cortar lo que dependía de esa sesión (los streams).
-- name: DeleteUserSession :one
DELETE FROM sessions
WHERE id = $1 AND user_id = $2 AND token_hash <> $3
RETURNING token_hash;

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
