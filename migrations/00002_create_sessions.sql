-- +goose Up
CREATE TABLE sessions (
    -- Se guarda el SHA-256 del token, nunca el token: si alguien lee la base, no puede usar las sesiones.
    token_hash   bytea       PRIMARY KEY,
    -- id es el identificador público de la sesión (GET /auth/sessions): el hash nunca sale de la API.
    id           bigint      NOT NULL GENERATED ALWAYS AS IDENTITY UNIQUE,
    user_id      bigint      NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    -- Para que el usuario reconozca sus dispositivos: el navegador y la IP con la que inició sesión.
    user_agent   text        NOT NULL DEFAULT '',
    ip           text        NOT NULL DEFAULT '',
    expires_at   timestamptz NOT NULL,
    -- Última actividad; se actualiza como mucho cada 5 minutos (ver auth.requireSession).
    last_seen_at timestamptz NOT NULL DEFAULT now(),
    created_at   timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX sessions_user_id_idx ON sessions (user_id);
CREATE INDEX sessions_expires_at_idx ON sessions (expires_at);

-- +goose Down
DROP TABLE sessions;
