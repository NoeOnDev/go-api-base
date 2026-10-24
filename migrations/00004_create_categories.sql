-- +goose Up
CREATE TABLE categories (
    id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name       text        NOT NULL,
    -- slug: identificador legible para URLs (por ejemplo "bebidas-calientes"). Debe ser único.
    slug       text        NOT NULL UNIQUE,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE categories;
