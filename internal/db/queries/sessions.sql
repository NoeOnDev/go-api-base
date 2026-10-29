-- name: CreateSession :exec
INSERT INTO sessions (token_hash, user_id, expires_at, user_agent, ip)
VALUES ($1, $2, $3, $4, $5);

-- GetSessionUser finds the valid session of the token, with its user. active_since is the
-- idle expiry: a session without activity since then no longer works
-- (without idle expiry, a minimum date).
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

-- ListUserSessions returns the sessions that still work: neither expired nor idle since before
-- active_since, as in GetSessionUser.
-- name: ListUserSessions :many
SELECT id, user_agent, ip, created_at, last_seen_at
FROM sessions
WHERE user_id = sqlc.arg('user_id') AND expires_at > now()
  AND last_seen_at > sqlc.arg('active_since')::timestamptz
ORDER BY last_seen_at DESC, id DESC;

-- DeleteUserSession closes a session of the user, except the given one (the request's).
-- It returns the token hash, to cut what depended on that session (the streams).
-- name: DeleteUserSession :one
DELETE FROM sessions
WHERE id = $1 AND user_id = $2 AND token_hash <> $3
RETURNING token_hash;

-- name: DeleteSession :exec
DELETE FROM sessions
WHERE token_hash = $1;

-- DeleteExpiredSessions deletes the expired ones and the ones idle since before inactive_before
-- (without idle expiry, a minimum date).
-- name: DeleteExpiredSessions :execrows
DELETE FROM sessions
WHERE expires_at <= now() OR last_seen_at <= sqlc.arg('inactive_before')::timestamptz;

-- name: DeleteUserSessions :exec
DELETE FROM sessions
WHERE user_id = $1;

-- name: DeleteOtherSessions :exec
DELETE FROM sessions
WHERE user_id = $1 AND token_hash <> $2;
