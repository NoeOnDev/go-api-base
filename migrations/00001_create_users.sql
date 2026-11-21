-- +goose Up
-- The roles: what an account can do (see "Roles" in docs/auth.md). Two come with the template and
-- cannot be changed: user, without permissions, and admin, with all of them. An administrator
-- creates the others (cashier, auditor) and gives each one its permissions.
CREATE TABLE roles (
    -- The identifier, which the users row and the API carry: 'catalog_manager'.
    key        text        PRIMARY KEY CONSTRAINT roles_key_format CHECK (key ~ '^[a-z][a-z0-9_]{1,39}$'),
    -- The name an administrator reads.
    name       text        NOT NULL,
    built_in   boolean     NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- These rows are part of the schema: the code names them (auth.RoleUser, auth.RoleAdmin), and the
-- tests keep them when they empty the tables (testutil.NewDB).
INSERT INTO roles (key, name, built_in) VALUES ('user', 'User', true), ('admin', 'Administrator', true);

-- The permissions of each role. A permission is a name the code defines ('products.manage', see
-- Service.RequirePermission): one the code no longer has is ignored.
CREATE TABLE role_permissions (
    role       text NOT NULL REFERENCES roles (key) ON DELETE CASCADE,
    permission text NOT NULL,
    PRIMARY KEY (role, permission)
);

CREATE TABLE users (
    id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name          text        NOT NULL,
    -- The application stores the email in lowercase and the database demands it, so UNIQUE is enough to
    -- prevent duplicates even if someone inserts by hand (Alice@x.com and alice@x.com would be two accounts).
    email         text        NOT NULL UNIQUE CONSTRAINT users_email_lowercase CHECK (email = lower(email)),
    -- NULL while an invitation is pending: the user chooses their password when accepting it
    -- (see "Invitations" in docs/auth.md). Nobody can log in without one.
    password_hash text,
    -- A role in use cannot be deleted (409 role_in_use).
    role          text        NOT NULL DEFAULT 'user' REFERENCES roles (key) ON DELETE RESTRICT,
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

-- The users of a role (GET /admin/users?role=, how many each role has) and the foreign key.
CREATE INDEX users_role_idx ON users (role);

-- +goose Down
DROP TABLE users;
DROP TABLE role_permissions;
DROP TABLE roles;
