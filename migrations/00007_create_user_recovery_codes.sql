-- +goose Up
-- Códigos de recuperación del 2FA. Se guardan como HMAC-SHA256 (con una clave derivada de
-- ENCRYPTION_KEY), nunca en texto plano. Cada código se borra al usarlo.
-- key_id indica con qué clave se calculó el HMAC: hace falta para saber, durante una rotación,
-- qué usuarios todavía tienen códigos que dependen de una clave vieja.
CREATE TABLE user_recovery_codes (
    code_hash  bytea       PRIMARY KEY,
    user_id    bigint      NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    key_id     text        NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX user_recovery_codes_user_id_idx ON user_recovery_codes (user_id);
CREATE INDEX user_recovery_codes_key_id_idx ON user_recovery_codes (key_id);

-- +goose Down
DROP TABLE user_recovery_codes;
