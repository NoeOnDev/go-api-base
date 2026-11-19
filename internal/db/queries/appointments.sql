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
ORDER BY a.starts_at, a.id
LIMIT sqlc.arg('limit') OFFSET sqlc.arg('offset');

-- name: CountBranchAppointments :one
SELECT count(*) FROM appointments
WHERE branch_id = sqlc.arg('branch_id') AND starts_at >= sqlc.arg('start') AND starts_at < sqlc.arg('end');

-- ListBookedRanges returns the booked appointments of a branch that overlap [start, end), to know
-- which slots are taken. It is written like the appointments_no_overlap constraint, with && on the
-- range, to use its index: with starts_at < end AND ends_at > start, PostgreSQL would read every
-- past appointment of the branch.
-- name: ListBookedRanges :many
SELECT starts_at, ends_at
FROM appointments
WHERE branch_id = sqlc.arg('branch_id') AND status = 'booked'
  AND tstzrange(starts_at, ends_at) && tstzrange(sqlc.arg('start')::timestamptz, sqlc.arg('end')::timestamptz);

-- CountUpcomingAppointments counts the booked appointments of a user that have not started yet, for
-- the limit per person.
-- name: CountUpcomingAppointments :one
SELECT count(*) FROM appointments
WHERE user_id = $1 AND status = 'booked' AND starts_at > now();

-- MarkAppointmentReminded records that the reminder was sent, so a second run of its job does not
-- send it again.
-- name: MarkAppointmentReminded :exec
UPDATE appointments
SET reminded_at = now()
WHERE id = $1 AND reminded_at IS NULL;

-- CancelAppointment cancels an appointment; one already canceled stays as it was.
-- name: CancelAppointment :exec
UPDATE appointments
SET status = 'canceled', updated_at = now()
WHERE id = $1 AND status = 'booked';
