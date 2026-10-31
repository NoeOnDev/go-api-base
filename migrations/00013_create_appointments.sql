-- +goose Up
-- Example of appointments (internal/appointments): bookings at a branch, within its business hours.
-- See "Modeling schedules" in docs/time.md.

-- btree_gist lets an EXCLUDE constraint compare branch_id (a plain value) together with a range.
-- It comes with PostgreSQL, and the owner of the database can install it (since PostgreSQL 13).
-- The Down does not remove it: other tables may use it.
CREATE EXTENSION IF NOT EXISTS btree_gist;

CREATE TABLE appointments (
    id              bigint      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    -- A branch with appointments cannot be deleted (409 branch_in_use).
    branch_id       bigint      NOT NULL REFERENCES branches (id) ON DELETE RESTRICT,
    user_id         bigint      NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    -- The hour of the branch's clock and its zone, as it was booked. If the rules of the zone change,
    -- the instants are computed again from them (see docs/time.md).
    local_starts_at timestamp   NOT NULL,
    time_zone       text        NOT NULL,
    -- The instants, computed from the two above: to search, sort and avoid overlaps.
    starts_at       timestamptz NOT NULL,
    ends_at         timestamptz NOT NULL CHECK (ends_at > starts_at),
    status          text        NOT NULL DEFAULT 'booked' CHECK (status IN ('booked', 'canceled')),
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    -- Two booked appointments of the same branch cannot overlap, even if they arrive at the same time
    -- to different instances of the API: the second one fails (409 slot_taken). The canceled ones do
    -- not count. With one agenda per person, it would be (staff_id WITH =, …).
    CONSTRAINT appointments_no_overlap EXCLUDE USING gist (
        branch_id WITH =,
        tstzrange(starts_at, ends_at) WITH &&
    ) WHERE (status = 'booked')
);

CREATE INDEX appointments_user_id_idx ON appointments (user_id, starts_at);
-- The agenda of a branch (canceled ones included) and the check when deleting a branch: the index
-- of the constraint only has the booked ones.
CREATE INDEX appointments_branch_id_idx ON appointments (branch_id, starts_at);

-- +goose Down
DROP TABLE appointments;
