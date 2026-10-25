-- +goose Up
-- Notificaciones dentro del sistema (ver internal/notifications). kind es el tipo, que el frontend
-- usa para elegir cómo mostrarla; data, lo que necesita para hacerlo. read_at NULL = sin leer.
CREATE TABLE notifications (
    id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id    bigint      NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    kind       text        NOT NULL,
    data       jsonb       NOT NULL DEFAULT '{}',
    read_at    timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);

-- Todas las consultas filtran por usuario; el índice parcial cuenta rápido las no leídas.
CREATE INDEX notifications_user_id_idx ON notifications (user_id, id DESC);
CREATE INDEX notifications_unread_idx ON notifications (user_id) WHERE read_at IS NULL;

-- +goose Down
DROP TABLE notifications;
