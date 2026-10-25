-- name: CreateNotification :one
INSERT INTO notifications (user_id, kind, data)
VALUES ($1, $2, $3)
RETURNING id, user_id, kind, data, read_at, created_at;

-- name: ListNotifications :many
SELECT id, user_id, kind, data, read_at, created_at
FROM notifications
WHERE user_id = sqlc.arg('user_id') AND (NOT sqlc.arg('unread_only')::bool OR read_at IS NULL)
ORDER BY id DESC
LIMIT sqlc.arg('limit') OFFSET sqlc.arg('offset');

-- name: CountNotifications :one
SELECT count(*) FROM notifications
WHERE user_id = sqlc.arg('user_id') AND (NOT sqlc.arg('unread_only')::bool OR read_at IS NULL);

-- name: CountUnreadNotifications :one
SELECT count(*) FROM notifications
WHERE user_id = $1 AND read_at IS NULL;

-- Si ya estaba leída, conserva la fecha original.
-- name: MarkNotificationRead :one
UPDATE notifications
SET read_at = coalesce(read_at, now())
WHERE id = $1 AND user_id = $2
RETURNING id, user_id, kind, data, read_at, created_at;

-- name: MarkAllNotificationsRead :execrows
UPDATE notifications
SET read_at = now()
WHERE user_id = $1 AND read_at IS NULL;

-- DeleteReadNotificationsBefore borra las leídas antes de la fecha (la limpieza periódica).
-- name: DeleteReadNotificationsBefore :execrows
DELETE FROM notifications
WHERE read_at < sqlc.arg('before')::timestamptz;
