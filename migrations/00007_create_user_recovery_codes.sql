-- +goose Up
-- 2FA recovery codes. They are stored as HMAC-SHA256 (with a key derived from
-- ENCRYPTION_KEY), never in plain text. Each code is deleted when used.
-- key_id says which key computed the HMAC: it is needed to know, during a rotation,
-- which users still have codes that depend on an old key.
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
