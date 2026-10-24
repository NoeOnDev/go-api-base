-- +goose Up
-- Códigos de recuperación del 2FA. Se guardan como HMAC-SHA256 (con una clave derivada de
-- ENCRYPTION_KEY), nunca en texto plano. Cada código se borra al usarlo.
CREATE TABLE user_recovery_codes (
    code_hash  bytea       PRIMARY KEY,
    user_id    bigint      NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX user_recovery_codes_user_id_idx ON user_recovery_codes (user_id);

-- +goose Down
DROP TABLE user_recovery_codes;
