-- +goose Up
-- Example of repeating classes (internal/classes): a class that repeats some days of the week at an
-- hour of the clock, and the changes to one session (canceled or moved). The sessions are computed
-- from the rule, not stored one by one. See "Modeling schedules" in docs/time.md.
CREATE TABLE classes (
    id               bigint      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name             text        NOT NULL,
    -- IANA zone of the place: the class is at start_time of its clock, in summer and in winter.
    time_zone        text        NOT NULL,
    -- The days of the week it repeats on; 0 is Sunday, like Go's time.Weekday.
    weekdays         smallint[]  NOT NULL CHECK (
        cardinality(weekdays) BETWEEN 1 AND 7 AND weekdays <@ ARRAY[0, 1, 2, 3, 4, 5, 6]::smallint[]
    ),
    start_time       time        NOT NULL,
    duration_minutes integer     NOT NULL CHECK (duration_minutes BETWEEN 5 AND 720),
    -- The first and the last day; without ends_on, it repeats with no end.
    starts_on        date        NOT NULL,
    ends_on          date        CHECK (ends_on >= starts_on),
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now()
);

-- The changes to one session: canceled, or moved to another hour (or day) of the clock.
CREATE TABLE class_session_changes (
    id              bigint    GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    class_id        bigint    NOT NULL REFERENCES classes (id) ON DELETE CASCADE,
    -- The date of the session in the rule: the one this change replaces.
    date            date      NOT NULL,
    canceled        boolean   NOT NULL DEFAULT false,
    -- Its new hour, if it was moved.
    local_starts_at timestamp,
    note            text      NOT NULL DEFAULT '',
    UNIQUE (class_id, date),
    CHECK (canceled <> (local_starts_at IS NOT NULL))
);

CREATE INDEX class_session_changes_moved_idx ON class_session_changes ((local_starts_at::date));

-- +goose Down
DROP TABLE class_session_changes;
DROP TABLE classes;
