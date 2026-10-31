-- +goose Up
-- Example of business hours (internal/branches): the branches of a business, each with its hours in
-- its own zone. See "Modeling schedules" in docs/time.md.
CREATE TABLE branches (
    id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name       text        NOT NULL,
    -- IANA zone of the place, like 'America/Bogota': the hours below are those of its clock.
    time_zone  text        NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- The weekly hours, one row per range: a day can have several (9:00-13:00 and 15:00-19:00). A range
-- whose closes_at is before its opens_at crosses midnight and ends the next day (18:00-02:00).
CREATE TABLE branch_hours (
    id        bigint   GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    branch_id bigint   NOT NULL REFERENCES branches (id) ON DELETE CASCADE,
    -- 0 is Sunday, like Go's time.Weekday and JavaScript's getDay().
    weekday   smallint NOT NULL CHECK (weekday BETWEEN 0 AND 6),
    opens_at  time     NOT NULL,
    closes_at time     NOT NULL CHECK (closes_at <> opens_at)
);

CREATE INDEX branch_hours_branch_id_idx ON branch_hours (branch_id, weekday, opens_at);

-- The exceptions of a date (a holiday, a day with other hours) replace that day's weekly hours.
-- Without hours, the branch is closed that day.
CREATE TABLE branch_exceptions (
    id        bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    branch_id bigint NOT NULL REFERENCES branches (id) ON DELETE CASCADE,
    date      date   NOT NULL,
    opens_at  time,
    closes_at time,
    note      text   NOT NULL DEFAULT '',
    UNIQUE (branch_id, date),
    CHECK ((opens_at IS NULL) = (closes_at IS NULL)),
    CHECK (opens_at IS NULL OR opens_at <> closes_at)
);

-- +goose Down
DROP TABLE branch_exceptions;
DROP TABLE branch_hours;
DROP TABLE branches;
