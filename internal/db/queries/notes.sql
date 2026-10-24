-- name: ListNotes :many
SELECT id, title, body, created_at, updated_at
FROM notes
ORDER BY id DESC
LIMIT sqlc.arg('limit') OFFSET sqlc.arg('offset');

-- name: CountNotes :one
SELECT count(*) FROM notes;

-- name: GetNote :one
SELECT id, title, body, created_at, updated_at
FROM notes
WHERE id = $1;

-- name: CreateNote :one
INSERT INTO notes (title, body)
VALUES ($1, $2)
RETURNING id, title, body, created_at, updated_at;

-- name: UpdateNote :one
UPDATE notes
SET title = $2, body = $3, updated_at = now()
WHERE id = $1
RETURNING id, title, body, created_at, updated_at;

-- name: DeleteNote :execrows
DELETE FROM notes
WHERE id = $1;
