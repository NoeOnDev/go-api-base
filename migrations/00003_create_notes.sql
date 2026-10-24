-- +goose Up
CREATE TABLE notes (
    id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    -- Cada nota pertenece a un usuario; si se borra el usuario, se borran sus notas.
    user_id    bigint      NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    title      text        NOT NULL,
    body       text        NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- Todas las consultas filtran por usuario.
CREATE INDEX notes_user_id_idx ON notes (user_id, id DESC);

-- +goose Down
DROP TABLE notes;
