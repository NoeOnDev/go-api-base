-- +goose Up
CREATE TABLE notes (
    id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    -- Each note belongs to a user; if the user is deleted, their notes are deleted.
    user_id    bigint      NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    title      text        NOT NULL,
    body       text        NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- Every query filters by user.
CREATE INDEX notes_user_id_idx ON notes (user_id, id DESC);

-- +goose Down
DROP TABLE notes;
