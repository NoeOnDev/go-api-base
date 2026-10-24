-- ReplaceRecoveryCodes borra los códigos del usuario e inserta los nuevos en una sola
-- sentencia, así que es atómico: nunca quedan los viejos y los nuevos a la vez.
-- name: ReplaceRecoveryCodes :exec
WITH deleted AS (
    DELETE FROM user_recovery_codes WHERE user_id = sqlc.arg('user_id')
)
INSERT INTO user_recovery_codes (code_hash, user_id, key_id)
SELECT unnest(sqlc.arg('code_hashes')::bytea[]), sqlc.arg('user_id'), sqlc.arg('key_id');

-- name: DeleteRecoveryCodes :exec
DELETE FROM user_recovery_codes
WHERE user_id = $1;

-- ConsumeRecoveryCode borra el código si es del usuario. Recibe el HMAC calculado con cada
-- clave del llavero, así funciona también con códigos generados antes de una rotación.
-- name: ConsumeRecoveryCode :execrows
DELETE FROM user_recovery_codes
WHERE user_id = sqlc.arg('user_id') AND code_hash = ANY(sqlc.arg('code_hashes')::bytea[]);

-- CountRecoveryCodeUsersByKey cuenta cuántos usuarios tienen códigos de cada clave.
-- name: CountRecoveryCodeUsersByKey :many
SELECT key_id, count(DISTINCT user_id) AS users
FROM user_recovery_codes
GROUP BY key_id
ORDER BY key_id;
