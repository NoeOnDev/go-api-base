-- GetWorkerTimeZone returns the zone of a user (NULL: the application's). It also says whether the
-- user exists.
-- name: GetWorkerTimeZone :one
SELECT time_zone FROM users
WHERE id = $1;

-- name: CreateShift :one
INSERT INTO shifts (user_id, time_zone, local_starts_at, local_ends_at, starts_at, ends_at, note)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- UpdateShift replaces a shift, also its person: reassigning it checks that it does not overlap the
-- shifts of the new one.
-- name: UpdateShift :one
UPDATE shifts
SET user_id = $2, time_zone = $3, local_starts_at = $4, local_ends_at = $5, starts_at = $6, ends_at = $7,
    note = $8, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: GetShift :one
SELECT * FROM shifts
WHERE id = $1;

-- name: DeleteShift :execrows
DELETE FROM shifts
WHERE id = $1;

-- ListShifts returns the shifts that overlap [start, end), of one user or of everyone.
-- name: ListShifts :many
SELECT * FROM shifts
WHERE (sqlc.narg('user_id')::bigint IS NULL OR user_id = sqlc.narg('user_id'))
  AND starts_at < sqlc.arg('end') AND ends_at > sqlc.arg('start')
ORDER BY starts_at, id;

-- CurrentShift returns the user's shift going on now, or starting within the next hour (clocking in
-- a bit early is common).
-- name: CurrentShift :one
SELECT id FROM shifts
WHERE user_id = $1 AND starts_at - interval '1 hour' <= now() AND ends_at > now()
ORDER BY starts_at
LIMIT 1;

-- name: ClockIn :one
INSERT INTO time_entries (user_id, shift_id)
VALUES ($1, $2)
RETURNING *;

-- name: ClockOut :one
UPDATE time_entries
SET clock_out = now()
WHERE user_id = $1 AND clock_out IS NULL
RETURNING *;

-- ListTimeEntries returns a user's entries that overlap [start, end); an open one counts until now.
-- name: ListTimeEntries :many
SELECT * FROM time_entries
WHERE user_id = sqlc.arg('user_id') AND clock_in < sqlc.arg('end')::timestamptz
  AND coalesce(clock_out, now()) > sqlc.arg('start')::timestamptz
ORDER BY clock_in, id;
