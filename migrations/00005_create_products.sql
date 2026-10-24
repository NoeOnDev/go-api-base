-- +goose Up
CREATE TABLE products (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    -- RESTRICT: no se puede borrar una categoría que todavía tiene productos.
    category_id bigint      NOT NULL REFERENCES categories (id) ON DELETE RESTRICT,
    name        text        NOT NULL,
    slug        text        NOT NULL UNIQUE,
    description text        NOT NULL DEFAULT '',
    -- El precio se guarda en centavos (un entero) para evitar errores de redondeo:
    -- $12.50 se guarda como 1250.
    price_cents bigint      NOT NULL CHECK (price_cents >= 0),
    stock       integer     NOT NULL DEFAULT 0 CHECK (stock >= 0),
    active      boolean     NOT NULL DEFAULT true,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX products_category_id_idx ON products (category_id);

-- +goose Down
DROP TABLE products;
