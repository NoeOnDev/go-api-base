-- +goose Up
CREATE TABLE products (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    -- RESTRICT: a category that still has products cannot be deleted.
    category_id bigint      NOT NULL REFERENCES categories (id) ON DELETE RESTRICT,
    name        text        NOT NULL,
    slug        text        NOT NULL UNIQUE,
    description text        NOT NULL DEFAULT '',
    -- The price is stored in cents (an integer) to avoid rounding errors:
    -- $12.50 is stored as 1250.
    price_cents bigint      NOT NULL CHECK (price_cents >= 0),
    stock       integer     NOT NULL DEFAULT 0 CHECK (stock >= 0),
    active      boolean     NOT NULL DEFAULT true,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX products_category_id_idx ON products (category_id);

-- +goose Down
DROP TABLE products;
