-- name: ListCategories :many
SELECT id, name, slug, created_at, updated_at
FROM categories
ORDER BY name, id
LIMIT sqlc.arg('limit') OFFSET sqlc.arg('offset');

-- name: CountCategories :one
SELECT count(*) FROM categories;

-- name: GetCategory :one
SELECT id, name, slug, created_at, updated_at
FROM categories
WHERE id = $1;

-- name: CreateCategory :one
INSERT INTO categories (name, slug)
VALUES ($1, $2)
RETURNING id, name, slug, created_at, updated_at;

-- name: UpdateCategory :one
UPDATE categories
SET name = $2, slug = $3, updated_at = now()
WHERE id = $1
RETURNING id, name, slug, created_at, updated_at;

-- name: DeleteCategory :execrows
DELETE FROM categories
WHERE id = $1;
