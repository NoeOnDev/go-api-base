-- +goose Up
CREATE TABLE sessions (
    -- The SHA-256 of the token is stored, never the token: whoever reads the database cannot use the sessions.
    token_hash   bytea       PRIMARY KEY,
    -- id is the public identifier of the session (GET /auth/sessions): the hash never leaves the API.
    id           bigint      NOT NULL GENERATED ALWAYS AS IDENTITY UNIQUE,
    user_id      bigint      NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    -- So the user recognizes their devices: the browser and the IP the session started from.
    user_agent   text        NOT NULL DEFAULT '',
    ip           text        NOT NULL DEFAULT '',
    expires_at   timestamptz NOT NULL,
    -- Last activity; it is updated at most every 5 minutes (see auth.requireSession).
    last_seen_at timestamptz NOT NULL DEFAULT now(),
    created_at   timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX sessions_user_id_idx ON sessions (user_id);
CREATE INDEX sessions_expires_at_idx ON sessions (expires_at);

-- +goose Down
DROP TABLE sessions;
