-- name: ListClasses :many
SELECT * FROM classes
ORDER BY name, id
LIMIT sqlc.arg('limit') OFFSET sqlc.arg('offset');

-- name: CountClasses :one
SELECT count(*) FROM classes;

-- name: GetClass :one
SELECT * FROM classes
WHERE id = $1;

-- name: CreateClass :one
INSERT INTO classes (name, time_zone, weekdays, start_time, duration_minutes, starts_on, ends_on)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: UpdateClass :one
UPDATE classes
SET name = $2, time_zone = $3, weekdays = $4, start_time = $5, duration_minutes = $6, starts_on = $7,
    ends_on = $8, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: DeleteClass :execrows
DELETE FROM classes
WHERE id = $1;

-- ListClassesBetween returns the classes that can have sessions between two dates: those in effect
-- then, and those with a session moved into those days.
-- name: ListClassesBetween :many
SELECT * FROM classes c
WHERE (c.starts_on <= sqlc.arg('to')::date AND (c.ends_on IS NULL OR c.ends_on >= sqlc.arg('from')::date))
   OR c.id IN (
       SELECT class_id FROM class_session_changes
       WHERE local_starts_at::date BETWEEN sqlc.arg('from')::date AND sqlc.arg('to')::date
   )
ORDER BY c.name, c.id;

-- ListSessionChanges returns the changes of some classes that touch the days between two dates: by
-- the date of the session, or by the date it was moved to.
-- name: ListSessionChanges :many
SELECT class_id, date, canceled, local_starts_at, note
FROM class_session_changes
WHERE class_id = ANY(sqlc.arg('class_ids')::bigint[])
  AND (date BETWEEN sqlc.arg('from')::date AND sqlc.arg('to')::date
       OR local_starts_at::date BETWEEN sqlc.arg('from')::date AND sqlc.arg('to')::date)
ORDER BY class_id, date;

-- name: UpsertSessionChange :one
INSERT INTO class_session_changes (class_id, date, canceled, local_starts_at, note)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (class_id, date) DO UPDATE
SET canceled = excluded.canceled, local_starts_at = excluded.local_starts_at, note = excluded.note
RETURNING class_id, date, canceled, local_starts_at, note;

-- name: DeleteSessionChange :execrows
DELETE FROM class_session_changes
WHERE class_id = $1 AND date = $2;
