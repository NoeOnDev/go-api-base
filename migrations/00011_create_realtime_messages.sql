-- +goose Up
-- Mensajes entre las instancias de la API que no entran en un NOTIFY, que admite menos de 8000
-- bytes (por ejemplo, el evento de una nota larga). Se guardan acá, y por NOTIFY viaja solo su id
-- (ver internal/realtime/instances.go). Las demás instancias los leen en el acto, y la limpieza
-- de cada hora borra los viejos.
CREATE TABLE realtime_messages (
    id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    payload    text        NOT NULL, -- el mensaje en JSON, tal cual (jsonb reordenaría las claves)
    created_at timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE realtime_messages;
