-- +goose Up
-- Example of files attached to a resource (internal/notes/attachments.go): the files of each note.
-- See "Using a file in a resource" in docs/files.md.
CREATE TABLE note_attachments (
    note_id    bigint      NOT NULL REFERENCES notes (id) ON DELETE CASCADE,
    -- CASCADE: when the cleanup deletes the file, the note stops listing it.
    file_id    bigint      NOT NULL REFERENCES files (id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (note_id, file_id)
);

-- The foreign key: deleting a file looks for the notes that have it.
CREATE INDEX note_attachments_file_id_idx ON note_attachments (file_id);

-- +goose Down
DROP TABLE note_attachments;
