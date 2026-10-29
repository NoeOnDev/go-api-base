-- ListenRealtime receives the messages between the brokers of the API instances (see
-- internal/realtime/instances.go).
-- name: ListenRealtime :exec
LISTEN realtime;

-- NotifyRealtime sends a message to every instance.
-- name: NotifyRealtime :exec
SELECT pg_notify('realtime', sqlc.arg('payload')::text);

-- NotifyStoredRealtimeMessage stores a message that does not fit in a NOTIFY and sends only its id.
-- The NOTIFY goes out when the statement ends, so the message is already stored when it arrives.
-- name: NotifyStoredRealtimeMessage :exec
WITH m AS (
    INSERT INTO realtime_messages (payload) VALUES (sqlc.arg('payload')) RETURNING id
)
SELECT pg_notify('realtime', json_build_object('origin', sqlc.arg('origin')::text, 'stored_id', m.id)::text)
FROM m;

-- name: GetRealtimeMessage :one
SELECT payload FROM realtime_messages
WHERE id = $1;

-- name: DeleteRealtimeMessagesBefore :execrows
DELETE FROM realtime_messages
WHERE created_at < $1;
