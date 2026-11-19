-- name: ListNotes :many
SELECT id, user_id, title, body, created_at, updated_at
FROM notes
WHERE user_id = sqlc.arg('user_id')
ORDER BY id DESC
LIMIT sqlc.arg('limit') OFFSET sqlc.arg('offset');

-- name: CountNotes :one
SELECT count(*) FROM notes
WHERE user_id = $1;

-- name: GetNote :one
SELECT id, user_id, title, body, created_at, updated_at
FROM notes
WHERE id = $1 AND user_id = $2;

-- name: CreateNote :one
INSERT INTO notes (user_id, title, body)
VALUES ($1, $2, $3)
RETURNING id, user_id, title, body, created_at, updated_at;

-- name: UpdateNote :one
UPDATE notes
SET title = $3, body = $4, updated_at = now()
WHERE id = $1 AND user_id = $2
RETURNING id, user_id, title, body, created_at, updated_at;

-- name: DeleteNote :execrows
DELETE FROM notes
WHERE id = $1 AND user_id = $2;

-- GetNoteID tells whether the note is the user's, without reading it.
-- name: GetNoteID :one
SELECT id FROM notes
WHERE id = $1 AND user_id = $2;

-- name: CreateNoteAttachment :exec
INSERT INTO note_attachments (note_id, file_id)
VALUES ($1, $2);

-- ListNoteAttachments returns the files of a note that can still be served, the oldest first.
-- name: ListNoteAttachments :many
SELECT f.*
FROM note_attachments a
JOIN files f ON f.id = a.file_id
WHERE a.note_id = $1 AND f.deleted_at IS NULL
ORDER BY a.created_at, f.id;

-- ListNoteAttachmentIDs returns the files of a note, to release them when it is deleted.
-- name: ListNoteAttachmentIDs :many
SELECT file_id FROM note_attachments
WHERE note_id = $1;

-- DeleteNoteAttachment takes a file off a note of the user, and returns whether it was there.
-- name: DeleteNoteAttachment :execrows
DELETE FROM note_attachments a
USING notes n
WHERE a.note_id = $1 AND a.file_id = $2 AND n.id = a.note_id AND n.user_id = $3;
