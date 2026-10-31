-- +goose Up
CREATE TABLE users (
    id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name          text        NOT NULL,
    -- The application stores the email in lowercase and the database demands it, so UNIQUE is enough to
    -- prevent duplicates even if someone inserts by hand (Alice@x.com and alice@x.com would be two accounts).
    email         text        NOT NULL UNIQUE CONSTRAINT users_email_lowercase CHECK (email = lower(email)),
    -- NULL while an invitation is pending: the user chooses their password when accepting it
    -- (see "Invitations" in docs/auth.md). Nobody can log in without one.
    password_hash text,
    role          text        NOT NULL DEFAULT 'user' CHECK (role IN ('user', 'admin')),
    -- NULL until the user confirms their email with the link sent to them.
    email_verified_at timestamptz,
    -- 2FA: TOTP secret encrypted with ENCRYPTION_KEY. With totp_enabled_at NULL, 2FA is not on
    -- (the secret may be waiting for confirmation). totp_last_step prevents reusing a code.
    totp_secret       text,
    totp_enabled_at   timestamptz,
    totp_last_step    bigint,
    -- Language of the user's emails and notifications, for example 'es-MX'
    -- (see docs/i18n.md). NULL is the default language (DEFAULT_LOCALE).
    locale            text,
    -- Time zone of the user's dates (emails, reports by day), an IANA name like
    -- 'America/Mexico_City' (see docs/time.md). NULL is the application's (APP_TIME_ZONE).
    time_zone         text,
    -- An administrator suspended the account: it cannot log in and its sessions were closed.
    -- NULL is an active account.
    suspended_at      timestamptz,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE users;
