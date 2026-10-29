-- ReplaceRecoveryCodes deletes the user's codes and inserts the new ones in a single
-- statement, so it is atomic: the old and the new ones are never there at the same time.
-- name: ReplaceRecoveryCodes :exec
WITH deleted AS (
    DELETE FROM user_recovery_codes WHERE user_id = sqlc.arg('user_id')
)
INSERT INTO user_recovery_codes (code_hash, user_id, key_id)
SELECT unnest(sqlc.arg('code_hashes')::bytea[]), sqlc.arg('user_id'), sqlc.arg('key_id');

-- name: DeleteRecoveryCodes :exec
DELETE FROM user_recovery_codes
WHERE user_id = $1;

-- ConsumeRecoveryCode deletes the code if it belongs to the user. It takes the HMAC computed with each
-- key of the keyring, so it also works with codes generated before a rotation.
-- name: ConsumeRecoveryCode :execrows
DELETE FROM user_recovery_codes
WHERE user_id = sqlc.arg('user_id') AND code_hash = ANY(sqlc.arg('code_hashes')::bytea[]);

-- CountRecoveryCodeUsersByKey counts how many users have codes of each key.
-- name: CountRecoveryCodeUsersByKey :many
SELECT key_id, count(DISTINCT user_id) AS users
FROM user_recovery_codes
GROUP BY key_id
ORDER BY key_id;
