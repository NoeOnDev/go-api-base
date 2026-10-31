-- +goose Up
-- Example of shifts (internal/shifts): an administrator assigns shifts to the users, and each one
-- records when they start and stop working. See "Modeling schedules" in docs/time.md.

-- For the EXCLUDE constraint (see 00013_create_appointments.sql). The Down does not remove it.
CREATE EXTENSION IF NOT EXISTS btree_gist;

CREATE TABLE shifts (
    id              bigint      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id         bigint      NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    -- The hours of the clock of the place and its zone, as they were assigned. A shift that crosses
    -- midnight ends the next day (local_ends_at is on another date).
    time_zone       text        NOT NULL,
    local_starts_at timestamp   NOT NULL,
    local_ends_at   timestamp   NOT NULL,
    -- The instants, computed from them. A night shift on the night of a daylight saving time change
    -- lasts one hour less or more.
    starts_at       timestamptz NOT NULL,
    ends_at         timestamptz NOT NULL CHECK (ends_at > starts_at),
    note            text        NOT NULL DEFAULT '',
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    -- A person cannot have two shifts that overlap (409 shift_overlap). Its index also serves the
    -- searches by user_id, the foreign key.
    CONSTRAINT shifts_no_overlap EXCLUDE USING gist (
        user_id WITH =,
        tstzrange(starts_at, ends_at) WITH &&
    )
);

CREATE INDEX shifts_starts_at_idx ON shifts (starts_at);

-- When each person clocked in and out. clock_out is NULL while they are working.
CREATE TABLE time_entries (
    id         bigint      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id    bigint      NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    -- The shift it belongs to, if there was one at that time.
    shift_id   bigint      REFERENCES shifts (id) ON DELETE SET NULL,
    clock_in   timestamptz NOT NULL DEFAULT now(),
    clock_out  timestamptz CHECK (clock_out > clock_in),
    created_at timestamptz NOT NULL DEFAULT now()
);

-- Only one open entry per person: clocking in twice answers 409 already_clocked_in.
CREATE UNIQUE INDEX time_entries_one_open_idx ON time_entries (user_id) WHERE clock_out IS NULL;
CREATE INDEX time_entries_user_id_idx ON time_entries (user_id, clock_in);
-- Deleting a shift sets shift_id to NULL in its entries.
CREATE INDEX time_entries_shift_id_idx ON time_entries (shift_id);

-- +goose Down
DROP TABLE time_entries;
DROP TABLE shifts;
