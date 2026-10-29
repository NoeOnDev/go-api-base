-- +goose Up
-- One-time tokens: the ones sent by email (reset the password, verify the email,
-- confirm a new email) and the pending challenge of the login with 2FA.
-- As with sessions, the SHA-256 of the token is stored, never the token.
CREATE TABLE user_tokens (
    token_hash bytea       PRIMARY KEY,
    user_id    bigint      NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    purpose    text        NOT NULL CHECK (purpose IN ('password_reset', 'email_verification', 'email_change', 'two_factor_login')),
    -- The new email, only for email_change: it is applied only once it is confirmed.
    email      text,
    expires_at timestamptz NOT NULL,
    -- Attempts used: the 2FA challenge is deleted when it reaches the maximum (see auth.maxChallengeAttempts).
    attempts   int         NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX user_tokens_user_id_purpose_idx ON user_tokens (user_id, purpose);
CREATE INDEX user_tokens_expires_at_idx ON user_tokens (expires_at);

-- +goose Down
DROP TABLE user_tokens;
