-- name: CreateNotification :one
INSERT INTO notifications (user_id, kind, data)
VALUES ($1, $2, $3)
RETURNING id, user_id, kind, data, read_at, created_at;

-- ListNotifications returns a page from the newest, by cursor: after is the id of the last one
-- already shown (NULL for the first page). It uses the (user_id, id DESC) index, so every page
-- is just as fast.
-- name: ListNotifications :many
SELECT id, user_id, kind, data, read_at, created_at
FROM notifications
WHERE user_id = sqlc.arg('user_id') AND (NOT sqlc.arg('unread_only')::bool OR read_at IS NULL)
  AND (sqlc.narg('after')::bigint IS NULL OR id < sqlc.narg('after'))
ORDER BY id DESC
LIMIT sqlc.arg('limit');

-- name: CountUnreadNotifications :one
SELECT count(*) FROM notifications
WHERE user_id = $1 AND read_at IS NULL;

-- If it was already read, it keeps the original date.
-- name: MarkNotificationRead :one
UPDATE notifications
SET read_at = coalesce(read_at, now())
WHERE id = $1 AND user_id = $2
RETURNING id, user_id, kind, data, read_at, created_at;

-- name: MarkAllNotificationsRead :execrows
UPDATE notifications
SET read_at = now()
WHERE user_id = $1 AND read_at IS NULL;

-- DeleteReadNotificationsBefore deletes the ones read before the date (the periodic cleanup).
-- name: DeleteReadNotificationsBefore :execrows
DELETE FROM notifications
WHERE read_at < sqlc.arg('before')::timestamptz;
