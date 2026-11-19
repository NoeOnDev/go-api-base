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
-- shifts of the new one, and the entries of the previous person stop belonging to the shift (they
-- keep their hours). It is one statement: if the shift overlaps, the entries are left as they were.
-- name: UpdateShift :one
WITH detached AS (
    UPDATE time_entries SET shift_id = NULL
    WHERE shift_id = $1 AND user_id <> $2
)
UPDATE shifts
SET user_id = $2, time_zone = $3, local_starts_at = $4, local_ends_at = $5, starts_at = $6, ends_at = $7,
    note = $8, updated_at = now()
WHERE shifts.id = $1
RETURNING shifts.*;

-- name: GetShift :one
SELECT * FROM shifts
WHERE id = $1;

-- name: DeleteShift :execrows
DELETE FROM shifts
WHERE id = $1;

-- ListShifts returns the shifts that overlap [start, end), of one user or of everyone. It is written
-- like the shifts_no_overlap constraint, with && on the range, to use its index.
-- name: ListShifts :many
SELECT * FROM shifts
WHERE (sqlc.narg('user_id')::bigint IS NULL OR user_id = sqlc.narg('user_id'))
  AND tstzrange(starts_at, ends_at) && tstzrange(sqlc.arg('start')::timestamptz, sqlc.arg('end')::timestamptz)
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
-- The && on the range is written like the time_entries_range_idx index, to use it (the range of an
-- open entry has no end); the last condition leaves out an open entry when the days asked for have
-- not arrived yet.
-- name: ListTimeEntries :many
SELECT * FROM time_entries
WHERE user_id = sqlc.arg('user_id')
  AND tstzrange(clock_in, clock_out) && tstzrange(sqlc.arg('start')::timestamptz, sqlc.arg('end')::timestamptz)
  AND coalesce(clock_out, now()) > sqlc.arg('start')::timestamptz
ORDER BY clock_in, id;
