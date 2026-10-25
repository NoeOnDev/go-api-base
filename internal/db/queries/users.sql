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

-- UpdateTOTPSecret vuelve a cifrar el secreto con otra clave. Solo lo cambia si sigue siendo
-- el que se leyó, así no pisa un cambio hecho al mismo tiempo (por ejemplo, desactivar el 2FA).
-- name: UpdateTOTPSecret :execrows
UPDATE users
SET totp_secret = sqlc.arg('new_secret')
WHERE id = sqlc.arg('id') AND totp_secret = sqlc.arg('old_secret');

-- name: ListUsersWithTOTPSecret :many
SELECT id, totp_secret
FROM users
WHERE totp_secret IS NOT NULL
ORDER BY id;

-- LockUser bloquea la fila del usuario hasta el final de la transacción. Ordena operaciones
-- simultáneas sobre el mismo usuario (por ejemplo, dos pedidos de enlace de recuperación).
-- name: LockUser :exec
SELECT id FROM users
WHERE id = $1
FOR UPDATE;

-- UpdateUserEmail cambia el email, ya confirmado desde la dirección nueva.
-- name: UpdateUserEmail :one
UPDATE users
SET email = $2, email_verified_at = now(), updated_at = now()
WHERE id = $1
RETURNING id, name, email, password_hash, role, email_verified_at, totp_secret, totp_enabled_at, totp_last_step, created_at, updated_at;
