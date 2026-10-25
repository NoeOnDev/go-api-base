-- name: CreateUserToken :exec
INSERT INTO user_tokens (token_hash, user_id, purpose, expires_at, email)
VALUES ($1, $2, $3, $4, $5);

-- ConsumeEmailChangeToken borra el token de cambio de email y devuelve el usuario y el email
-- nuevo, en una sola operación (no se puede usar dos veces).
-- name: ConsumeEmailChangeToken :one
DELETE FROM user_tokens
WHERE token_hash = $1 AND purpose = 'email_change' AND expires_at > now()
RETURNING user_id, email::text;

-- ConsumeUserToken borra el token y devuelve su usuario en una sola operación, así que
-- dos peticiones simultáneas con el mismo token no pueden usarlo las dos.
-- name: ConsumeUserToken :one
DELETE FROM user_tokens
WHERE token_hash = $1 AND purpose = $2 AND expires_at > now()
RETURNING user_id;

-- name: DeleteUserTokens :exec
DELETE FROM user_tokens
WHERE user_id = $1 AND purpose = $2;

-- name: DeleteExpiredUserTokens :execrows
DELETE FROM user_tokens
WHERE expires_at <= now();

-- GetUserTokenUser busca un token vigente sin consumirlo (el desafío del 2FA sigue
-- sirviendo después de un código incorrecto, hasta agotar sus intentos).
-- name: GetUserTokenUser :one
SELECT user_id
FROM user_tokens
WHERE token_hash = sqlc.arg('token_hash') AND purpose = sqlc.arg('purpose') AND expires_at > now()
  AND attempts < sqlc.arg('max_attempts')::int;

-- UseUserTokenAttempt gasta un intento del token y devuelve cuántos lleva. Es una sola
-- sentencia, así que varias peticiones simultáneas no pueden pasarse del máximo.
-- name: UseUserTokenAttempt :one
UPDATE user_tokens
SET attempts = attempts + 1
WHERE token_hash = sqlc.arg('token_hash') AND purpose = sqlc.arg('purpose') AND expires_at > now()
  AND attempts < sqlc.arg('max_attempts')::int
RETURNING attempts;

-- name: DeleteUserToken :exec
DELETE FROM user_tokens
WHERE token_hash = $1;
