-- ListenRealtime recibe los mensajes entre los brokers de las instancias de la API (ver
-- internal/realtime/instances.go).
-- name: ListenRealtime :exec
LISTEN realtime;

-- NotifyRealtime envía un mensaje a todas las instancias.
-- name: NotifyRealtime :exec
SELECT pg_notify('realtime', sqlc.arg('payload')::text);

-- NotifyStoredRealtimeMessage guarda un mensaje que no entra en un NOTIFY y envía solo su id.
-- El NOTIFY sale al terminar la sentencia, así que el mensaje ya está guardado cuando llega.
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
