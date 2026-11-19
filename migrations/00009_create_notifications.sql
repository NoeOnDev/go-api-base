-- +goose Up
-- In-app notifications (see internal/notifications). kind is the kind, which the frontend
-- uses to choose how to show it; data, what it needs to do so. read_at NULL = unread.
CREATE TABLE notifications (
    id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id    bigint      NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    kind       text        NOT NULL,
    data       jsonb       NOT NULL DEFAULT '{}',
    read_at    timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);

-- Every query filters by user; the partial index counts the unread ones fast.
CREATE INDEX notifications_user_id_idx ON notifications (user_id, id DESC);
CREATE INDEX notifications_unread_idx ON notifications (user_id) WHERE read_at IS NULL;
-- The hourly cleanup deletes the ones read long ago: without this index it would read the whole
-- table each time, and the unread ones are never deleted, so the table only grows.
CREATE INDEX notifications_read_at_idx ON notifications (read_at) WHERE read_at IS NOT NULL;

-- +goose Down
DROP TABLE notifications;
