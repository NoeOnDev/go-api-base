-- name: CreateSession :exec
INSERT INTO sessions (token_hash, user_id, expires_at, user_agent, ip)
VALUES ($1, $2, $3, $4, $5);

-- GetSessionUser busca la sesión vigente del token, con su usuario. active_since es el
-- vencimiento por inactividad: una sesión sin actividad desde entonces ya no sirve
-- (sin vencimiento, una fecha mínima).
-- name: GetSessionUser :one
SELECT sqlc.embed(u), s.id AS session_id, s.last_seen_at
FROM sessions s
JOIN users u ON u.id = s.user_id
WHERE s.token_hash = sqlc.arg('token_hash') AND s.expires_at > now()
  AND s.last_seen_at > sqlc.arg('active_since')::timestamptz;

-- name: TouchSession :exec
UPDATE sessions
SET last_seen_at = now()
WHERE token_hash = $1;

-- ListUserSessions devuelve las sesiones que todavía sirven: sin vencer ni inactivas desde antes
-- de active_since, como en GetSessionUser.
-- name: ListUserSessions :many
SELECT id, user_agent, ip, created_at, last_seen_at
FROM sessions
WHERE user_id = sqlc.arg('user_id') AND expires_at > now()
  AND last_seen_at > sqlc.arg('active_since')::timestamptz
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

-- DeleteExpiredSessions borra las vencidas y las inactivas desde antes de inactive_before
-- (sin vencimiento por inactividad, una fecha mínima).
-- name: DeleteExpiredSessions :execrows
DELETE FROM sessions
WHERE expires_at <= now() OR last_seen_at <= sqlc.arg('inactive_before')::timestamptz;

-- name: DeleteUserSessions :exec
DELETE FROM sessions
WHERE user_id = $1;

-- name: DeleteOtherSessions :exec
DELETE FROM sessions
WHERE user_id = $1 AND token_hash <> $2;
