-- +goose Up
-- The uploaded files (see internal/files and docs/files.md). The bytes are in the store (the local
-- disk or S3, internal/storage) under key; this table says whose each file is and what it is.
--
-- A row lives longer than its bytes: it is created before uploading them and deleted after deleting
-- them, so nothing is left in the store that no row remembers. The hourly cleanup deletes the bytes
-- and then the row of the files nobody needs: see the files_purge_idx index.
CREATE TABLE files (
    id           bigint      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    -- Who uploaded it. SET NULL, not CASCADE: deleting the account must not delete the row before
    -- its bytes. The cleanup deletes both.
    user_id      bigint      REFERENCES users (id) ON DELETE SET NULL,
    -- Where the bytes are in the store. Random: never the name the user sent.
    key          text        NOT NULL UNIQUE,
    -- The name the user sent, cleaned, for the download. It is never a path.
    name         text        NOT NULL,
    -- The type found in the bytes, not the one the browser said.
    content_type text        NOT NULL,
    size         bigint      NOT NULL CHECK (size > 0),
    -- When the bytes finished arriving at the store. NULL: the upload is in progress, or was cut.
    uploaded_at  timestamptz,
    -- When a resource took it (files.Attach): a note, an avatar. NULL: uploaded, and nothing uses it
    -- yet. One that stays that way for a day is deleted.
    attached_at  timestamptz,
    -- When it was marked to be deleted (files.Release, DELETE /files/{id}). It is no longer served.
    deleted_at   timestamptz,
    created_at   timestamptz NOT NULL DEFAULT now()
);

-- The space a user takes (the quota), and the foreign key.
CREATE INDEX files_user_id_idx ON files (user_id);
-- What the hourly cleanup looks at: the marked ones, those of a deleted account and the ones nothing
-- uses. ListPurgeableFiles repeats this condition to use the index, so it does not read the files
-- in use, which are almost all of them.
CREATE INDEX files_purge_idx ON files (created_at)
    WHERE deleted_at IS NOT NULL OR user_id IS NULL OR attached_at IS NULL;

-- +goose Down
DROP TABLE files;
