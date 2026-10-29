-- +goose Up
-- Límites de intentos que valen para todas las instancias de la API: por cuenta en el login, por
-- usuario en el 2FA y en las acciones de la cuenta, y por destinatario en los correos (ver
-- internal/ratelimit). key es el SHA-256 del nombre del límite y la clave, que puede ser un email.
-- tat es el momento en que la clave vuelve a tener todos sus intentos (algoritmo GCRA).
CREATE TABLE rate_limits (
    key bytea       PRIMARY KEY,
    tat timestamptz NOT NULL
);

-- +goose Down
DROP TABLE rate_limits;
