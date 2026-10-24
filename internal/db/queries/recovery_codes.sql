-- ReplaceRecoveryCodes borra los códigos del usuario e inserta los nuevos en una sola
-- sentencia, así que es atómico: nunca quedan los viejos y los nuevos a la vez.
-- name: ReplaceRecoveryCodes :exec
WITH deleted AS (
    DELETE FROM user_recovery_codes WHERE user_id = sqlc.arg('user_id')
)
INSERT INTO user_recovery_codes (code_hash, user_id)
SELECT unnest(sqlc.arg('code_hashes')::bytea[]), sqlc.arg('user_id');

-- name: DeleteRecoveryCodes :exec
DELETE FROM user_recovery_codes
WHERE user_id = $1;

-- ConsumeRecoveryCode borra el código si es del usuario: 1 fila si era válido, 0 si no.
-- name: ConsumeRecoveryCode :execrows
DELETE FROM user_recovery_codes
WHERE user_id = $1 AND code_hash = $2;
