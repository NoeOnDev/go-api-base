-- TakeRateLimit spends one attempt of the key (see internal/ratelimit) with the GCRA algorithm. tat
-- is the moment the key has all its attempts again: each attempt moves it one
-- "every" forward, and it is allowed while tat is no more than burst × every from now.
-- It affects one row if the attempt was allowed, and none if not (then nothing changes). It is a
-- single statement, so two instances spending attempts at once do not step on each other.
-- name: TakeRateLimit :execrows
INSERT INTO rate_limits AS r (key, tat)
VALUES (sqlc.arg('key'), now() + sqlc.arg('every')::interval)
ON CONFLICT (key) DO UPDATE
SET tat = greatest(r.tat, now()) + sqlc.arg('every')::interval
WHERE greatest(r.tat, now()) + sqlc.arg('every')::interval <= now() + sqlc.arg('every')::interval * sqlc.arg('burst')::int;

-- name: DeleteRateLimit :exec
DELETE FROM rate_limits
WHERE key = $1;

-- DeleteExpiredRateLimits deletes the keys that already refilled all their attempts: they are the same
-- as having no row.
-- name: DeleteExpiredRateLimits :execrows
DELETE FROM rate_limits
WHERE tat <= now();
