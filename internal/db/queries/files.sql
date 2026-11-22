-- CreateFile records a file before its bytes are uploaded (uploaded_at is NULL).
-- name: CreateFile :one
INSERT INTO files (user_id, key, name, content_type, size)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- MarkFileUploaded records that the bytes are in the store: from here on the file can be served.
-- It finds nothing if the file was marked to be deleted while it was uploading (its account was
-- deleted): the cleanup deletes what arrived.
-- name: MarkFileUploaded :one
UPDATE files
SET uploaded_at = now()
WHERE id = $1 AND deleted_at IS NULL
RETURNING *;

-- GetFile returns a file that can be served: uploaded and not marked to be deleted.
-- name: GetFile :one
SELECT * FROM files
WHERE id = $1 AND uploaded_at IS NOT NULL AND deleted_at IS NULL;

-- SumUserFileSizes returns the bytes a user takes, for the quota. An upload in progress counts.
-- name: SumUserFileSizes :one
SELECT coalesce(sum(size), 0)::bigint FROM files
WHERE user_id = $1 AND deleted_at IS NULL;

-- AttachFiles marks the user's files as in use, and returns how many it marked: fewer than asked
-- means that one is not theirs, is not uploaded, is already in use or is marked to be deleted.
-- shared is true for a file that belongs to its resource and not to who uploaded it.
-- name: AttachFiles :execrows
UPDATE files
SET attached_at = now(), shared = sqlc.arg('shared')
WHERE id = ANY(sqlc.arg('ids')::bigint[]) AND user_id = sqlc.arg('user_id')
  AND uploaded_at IS NOT NULL AND attached_at IS NULL AND deleted_at IS NULL;

-- ReleaseFiles marks the files to be deleted: they are no longer served, and the cleanup deletes
-- their bytes and their rows.
-- name: ReleaseFiles :exec
UPDATE files
SET deleted_at = now()
WHERE id = ANY(sqlc.arg('ids')::bigint[]) AND deleted_at IS NULL;

-- ReleaseUnattachedFile marks a file of the user that nothing uses, and returns whether it did.
-- name: ReleaseUnattachedFile :execrows
UPDATE files
SET deleted_at = now()
WHERE id = $1 AND user_id = $2 AND attached_at IS NULL AND deleted_at IS NULL;

-- MarkUnusedFiles marks to be deleted the files nobody needs, and returns how many: those of a
-- deleted account that are not shared, and the ones that nothing took since before the date (an
-- upload that was cut, or a form that was never sent). It is one statement, so a file a resource
-- takes at the same time is either taken or marked, never both: AttachFiles does not take a marked
-- one. The first condition is the one of the files_unused_idx index, to use it.
-- name: MarkUnusedFiles :execrows
UPDATE files
SET deleted_at = now()
WHERE deleted_at IS NULL AND (attached_at IS NULL OR user_id IS NULL)
  AND ((user_id IS NULL AND NOT shared)
       OR (attached_at IS NULL AND created_at < sqlc.arg('unattached_before')::timestamptz));

-- ListPurgeableFiles returns the marked files, which the cleanup deletes. One whose upload did not
-- finish waits until the date: its bytes can still be on their way to the store, and deleting the
-- row first would leave them there without one.
-- name: ListPurgeableFiles :many
SELECT id, key FROM files
WHERE deleted_at IS NOT NULL
  AND (uploaded_at IS NOT NULL OR created_at < sqlc.arg('upload_started_before')::timestamptz)
ORDER BY id
LIMIT sqlc.arg('limit');

-- name: DeleteFile :exec
DELETE FROM files
WHERE id = $1;
