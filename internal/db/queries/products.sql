-- name: ListProducts :many
SELECT p.id, p.category_id, c.name AS category_name, p.name, p.slug, p.description,
       p.price_cents, p.stock, p.active, p.created_at, p.updated_at
FROM products p
JOIN categories c ON c.id = p.category_id
WHERE (sqlc.narg('category_id')::bigint IS NULL OR p.category_id = sqlc.narg('category_id'))
  AND (sqlc.narg('search')::text IS NULL OR p.name ILIKE '%' || sqlc.narg('search') || '%')
  AND (sqlc.narg('active')::boolean IS NULL OR p.active = sqlc.narg('active'))
ORDER BY p.id DESC
LIMIT sqlc.arg('limit') OFFSET sqlc.arg('offset');

-- name: CountProducts :one
SELECT count(*)
FROM products p
WHERE (sqlc.narg('category_id')::bigint IS NULL OR p.category_id = sqlc.narg('category_id'))
  AND (sqlc.narg('search')::text IS NULL OR p.name ILIKE '%' || sqlc.narg('search') || '%')
  AND (sqlc.narg('active')::boolean IS NULL OR p.active = sqlc.narg('active'));

-- name: GetProduct :one
SELECT p.id, p.category_id, c.name AS category_name, p.name, p.slug, p.description,
       p.price_cents, p.stock, p.active, p.created_at, p.updated_at
FROM products p
JOIN categories c ON c.id = p.category_id
WHERE p.id = $1;

-- name: CreateProduct :one
INSERT INTO products (category_id, name, slug, description, price_cents, stock, active)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING id;

-- name: UpdateProduct :one
UPDATE products
SET category_id = $2, name = $3, slug = $4, description = $5,
    price_cents = $6, stock = $7, active = $8, updated_at = now()
WHERE id = $1
RETURNING id;

-- name: DeleteProduct :execrows
DELETE FROM products
WHERE id = $1;
