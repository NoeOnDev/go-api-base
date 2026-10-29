-- TakeRateLimit gasta un intento de la clave (ver internal/ratelimit) con el algoritmo GCRA. tat
-- es el momento en que la clave vuelve a tener todos sus intentos: cada intento lo corre un
-- "every" hacia adelante, y se permite mientras tat no quede a más de burst × every de ahora.
-- Afecta una fila si el intento se permitió, y ninguna si no (en ese caso no cambia nada). Es una
-- sola sentencia, así que dos instancias que gastan intentos a la vez no se pisan.
-- name: TakeRateLimit :execrows
INSERT INTO rate_limits AS r (key, tat)
VALUES (sqlc.arg('key'), now() + sqlc.arg('every')::interval)
ON CONFLICT (key) DO UPDATE
SET tat = greatest(r.tat, now()) + sqlc.arg('every')::interval
WHERE greatest(r.tat, now()) + sqlc.arg('every')::interval <= now() + sqlc.arg('every')::interval * sqlc.arg('burst')::int;

-- name: DeleteRateLimit :exec
DELETE FROM rate_limits
WHERE key = $1;

-- DeleteExpiredRateLimits borra las claves que ya recargaron todos sus intentos: equivalen a no
-- tener fila.
-- name: DeleteExpiredRateLimits :execrows
DELETE FROM rate_limits
WHERE tat <= now();
