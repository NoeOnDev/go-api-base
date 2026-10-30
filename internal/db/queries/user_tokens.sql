-- name: CreateUserToken :exec
INSERT INTO user_tokens (token_hash, user_id, purpose, expires_at, email)
VALUES ($1, $2, $3, $4, $5);

-- ConsumeEmailChangeToken deletes the email change token and returns the user and the new
-- email, in a single operation (it cannot be used twice).
-- name: ConsumeEmailChangeToken :one
DELETE FROM user_tokens
WHERE token_hash = $1 AND purpose = 'email_change' AND expires_at > now()
RETURNING user_id, email::text;

-- ConsumeUserToken deletes the token and returns its user in a single operation, so
-- two simultaneous requests with the same token cannot both use it.
-- name: ConsumeUserToken :one
DELETE FROM user_tokens
WHERE token_hash = $1 AND purpose = $2 AND expires_at > now()
RETURNING user_id;

-- name: DeleteUserTokens :exec
DELETE FROM user_tokens
WHERE user_id = $1 AND purpose = $2;

-- DeleteAllUserTokens deletes every pending token of the user (links and two-factor challenges),
-- for example when the account is suspended.
-- name: DeleteAllUserTokens :exec
DELETE FROM user_tokens
WHERE user_id = $1;

-- name: DeleteExpiredUserTokens :execrows
DELETE FROM user_tokens
WHERE expires_at <= now();

-- GetUserTokenUser finds a valid token without consuming it (the 2FA challenge keeps
-- working after a wrong code, until its attempts run out).
-- name: GetUserTokenUser :one
SELECT user_id
FROM user_tokens
WHERE token_hash = sqlc.arg('token_hash') AND purpose = sqlc.arg('purpose') AND expires_at > now()
  AND attempts < sqlc.arg('max_attempts')::int;

-- UseUserTokenAttempt spends one attempt of the token and returns how many it has used. It is a single
-- statement, so several simultaneous requests cannot go over the maximum.
-- name: UseUserTokenAttempt :one
UPDATE user_tokens
SET attempts = attempts + 1
WHERE token_hash = sqlc.arg('token_hash') AND purpose = sqlc.arg('purpose') AND expires_at > now()
  AND attempts < sqlc.arg('max_attempts')::int
RETURNING attempts;

-- name: DeleteUserToken :exec
DELETE FROM user_tokens
WHERE token_hash = $1;
