-- +goose Up
-- Attempt limits shared by every instance of the API: per account in the login, per
-- user in the 2FA and the account actions, and per recipient in the emails (see
-- internal/ratelimit). key is the SHA-256 of the limit's name and the key, which can be an email.
-- tat is the moment the key has all its attempts again (GCRA algorithm).
CREATE TABLE rate_limits (
    key bytea       PRIMARY KEY,
    tat timestamptz NOT NULL
);

-- +goose Down
DROP TABLE rate_limits;
