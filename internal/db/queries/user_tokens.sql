-- name: CreateUserToken :exec
INSERT INTO user_tokens (token_hash, user_id, purpose, expires_at)
VALUES ($1, $2, $3, $4);

-- ConsumeUserToken borra el token y devuelve su usuario en una sola operación, así que
-- dos peticiones simultáneas con el mismo token no pueden usarlo las dos.
-- name: ConsumeUserToken :one
DELETE FROM user_tokens
WHERE token_hash = $1 AND purpose = $2 AND expires_at > now()
RETURNING user_id;

-- name: DeleteUserTokens :exec
DELETE FROM user_tokens
WHERE user_id = $1 AND purpose = $2;

-- name: DeleteExpiredUserTokens :exec
DELETE FROM user_tokens
WHERE expires_at <= now();
