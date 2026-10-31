-- name: ListBranches :many
SELECT id, name, time_zone, created_at, updated_at
FROM branches
ORDER BY name, id
LIMIT sqlc.arg('limit') OFFSET sqlc.arg('offset');

-- name: CountBranches :one
SELECT count(*) FROM branches;

-- name: GetBranch :one
SELECT id, name, time_zone, created_at, updated_at
FROM branches
WHERE id = $1;

-- LockBranch locks the branch while its hours are replaced, so two simultaneous changes do not mix.
-- name: LockBranch :one
SELECT id FROM branches
WHERE id = $1
FOR UPDATE;

-- name: CreateBranch :one
INSERT INTO branches (name, time_zone)
VALUES ($1, $2)
RETURNING id, name, time_zone, created_at, updated_at;

-- name: UpdateBranch :one
UPDATE branches
SET name = $2, time_zone = $3, updated_at = now()
WHERE id = $1
RETURNING id, name, time_zone, created_at, updated_at;

-- name: DeleteBranch :execrows
DELETE FROM branches
WHERE id = $1;

-- ListBranchHours returns the weekly hours of several branches (those of a page).
-- name: ListBranchHours :many
SELECT branch_id, weekday, opens_at, closes_at
FROM branch_hours
WHERE branch_id = ANY(sqlc.arg('branch_ids')::bigint[])
ORDER BY branch_id, weekday, opens_at;

-- name: DeleteBranchHours :exec
DELETE FROM branch_hours
WHERE branch_id = $1;

-- name: CreateBranchHours :exec
INSERT INTO branch_hours (branch_id, weekday, opens_at, closes_at)
VALUES ($1, $2, $3, $4);

-- name: ListBranchExceptions :many
SELECT date, opens_at, closes_at, note
FROM branch_exceptions
WHERE branch_id = sqlc.arg('branch_id') AND date BETWEEN sqlc.arg('from')::date AND sqlc.arg('to')::date
ORDER BY date;

-- name: UpsertBranchException :one
INSERT INTO branch_exceptions (branch_id, date, opens_at, closes_at, note)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (branch_id, date) DO UPDATE
SET opens_at = excluded.opens_at, closes_at = excluded.closes_at, note = excluded.note
RETURNING date, opens_at, closes_at, note;

-- name: DeleteBranchException :execrows
DELETE FROM branch_exceptions
WHERE branch_id = $1 AND date = $2;
