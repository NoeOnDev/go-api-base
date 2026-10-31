-- name: CreateAppointment :one
INSERT INTO appointments (branch_id, user_id, local_starts_at, time_zone, starts_at, ends_at)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: GetAppointment :one
SELECT sqlc.embed(a), b.name AS branch_name
FROM appointments a
JOIN branches b ON b.id = a.branch_id
WHERE a.id = $1;

-- ListUserAppointments returns a user's appointments that start between two instants (the days of
-- the range, in the user's zone).
-- name: ListUserAppointments :many
SELECT sqlc.embed(a), b.name AS branch_name
FROM appointments a
JOIN branches b ON b.id = a.branch_id
WHERE a.user_id = sqlc.arg('user_id') AND a.starts_at >= sqlc.arg('start') AND a.starts_at < sqlc.arg('end')
ORDER BY a.starts_at, a.id
LIMIT sqlc.arg('limit') OFFSET sqlc.arg('offset');

-- name: CountUserAppointments :one
SELECT count(*) FROM appointments
WHERE user_id = sqlc.arg('user_id') AND starts_at >= sqlc.arg('start') AND starts_at < sqlc.arg('end');

-- ListBranchAppointments returns the appointments of a branch that start between two instants (a
-- day of the branch), canceled ones included.
-- name: ListBranchAppointments :many
SELECT sqlc.embed(a), b.name AS branch_name
FROM appointments a
JOIN branches b ON b.id = a.branch_id
WHERE a.branch_id = sqlc.arg('branch_id') AND a.starts_at >= sqlc.arg('start') AND a.starts_at < sqlc.arg('end')
ORDER BY a.starts_at, a.id;

-- ListBookedRanges returns the booked appointments of a branch that overlap [start, end), to know
-- which slots are taken.
-- name: ListBookedRanges :many
SELECT starts_at, ends_at
FROM appointments
WHERE branch_id = sqlc.arg('branch_id') AND status = 'booked'
  AND starts_at < sqlc.arg('end') AND ends_at > sqlc.arg('start');

-- CancelAppointment cancels an appointment; one already canceled stays as it was.
-- name: CancelAppointment :exec
UPDATE appointments
SET status = 'canceled', updated_at = now()
WHERE id = $1 AND status = 'booked';
