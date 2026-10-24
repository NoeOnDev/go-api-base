-- name: CreateRecoveryCode :exec
INSERT INTO user_recovery_codes (code_hash, user_id)
VALUES ($1, $2);

-- name: DeleteRecoveryCodes :exec
DELETE FROM user_recovery_codes
WHERE user_id = $1;

-- ConsumeRecoveryCode borra el código si es del usuario: 1 fila si era válido, 0 si no.
-- name: ConsumeRecoveryCode :execrows
DELETE FROM user_recovery_codes
WHERE user_id = $1 AND code_hash = $2;

-- name: CountRecoveryCodes :one
SELECT count(*) FROM user_recovery_codes
WHERE user_id = $1;
