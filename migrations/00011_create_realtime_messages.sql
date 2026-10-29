-- +goose Up
-- Messages between the API instances that do not fit in a NOTIFY, which allows less than 8000
-- bytes (for example, the event of a long note). They are stored here, and only their id travels
-- by NOTIFY (see internal/realtime/instances.go). The other instances read them right away, and
-- the hourly cleanup deletes the old ones.
CREATE TABLE realtime_messages (
    id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    payload    text        NOT NULL, -- the message in JSON, as is (jsonb would reorder the keys)
    created_at timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE realtime_messages;
