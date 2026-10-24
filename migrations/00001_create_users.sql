-- +goose Up
CREATE TABLE users (
    id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name          text        NOT NULL,
    -- La aplicación guarda el email en minúsculas, así que UNIQUE alcanza para evitar duplicados.
    email         text        NOT NULL UNIQUE,
    password_hash text        NOT NULL,
    role          text        NOT NULL DEFAULT 'user' CHECK (role IN ('user', 'admin')),
    -- NULL mientras el usuario no confirme su email con el enlace que se le envía.
    email_verified_at timestamptz,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE users;
