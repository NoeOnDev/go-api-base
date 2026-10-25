-- +goose Up
-- Tokens de un solo uso: los que se envían por correo (recuperar la contraseña, verificar el email,
-- confirmar un email nuevo) y el desafío pendiente del login con 2FA.
-- Como en las sesiones, se guarda el SHA-256 del token, nunca el token.
CREATE TABLE user_tokens (
    token_hash bytea       PRIMARY KEY,
    user_id    bigint      NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    purpose    text        NOT NULL CHECK (purpose IN ('password_reset', 'email_verification', 'email_change', 'two_factor_login')),
    -- El email nuevo, solo para email_change: se aplica recién cuando se confirma.
    email      text,
    expires_at timestamptz NOT NULL,
    -- Intentos usados: el desafío del 2FA se borra al llegar al máximo (ver auth.maxChallengeAttempts).
    attempts   int         NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX user_tokens_user_id_purpose_idx ON user_tokens (user_id, purpose);
CREATE INDEX user_tokens_expires_at_idx ON user_tokens (expires_at);

-- +goose Down
DROP TABLE user_tokens;
