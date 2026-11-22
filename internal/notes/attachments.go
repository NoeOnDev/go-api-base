package notes

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"

	"github.com/jackc/pgx/v5"

	"github.com/NoeOnDev/go-api-base/internal/auth"
	"github.com/NoeOnDev/go-api-base/internal/db"
	"github.com/NoeOnDev/go-api-base/internal/files"
	"github.com/NoeOnDev/go-api-base/internal/httpx"
	"github.com/NoeOnDev/go-api-base/internal/store"
)

// The attachments of a note: the example of files used by a resource (see "Using a file in a
// resource" in docs/files.md). The note_attachments table links each note with its files, and the
// three steps are here: files.Service.Receive reads the upload, files.Attach marks it as in use in
// the transaction that links it, and files.Release marks it to be deleted when it is taken off (or,
// in delete, when the note goes).
//
// The files are downloaded from GET /files/{id}, which only lets their owner in: the attachments of
// a note are its owner's. A resource whose files others can see serves them from its own route,
// with files.Service.Serve after its own check.

// attachmentRule is what a note accepts: images and documents up to 5 MB.
var attachmentRule = files.Rule{MaxBytes: 5 << 20, Types: slices.Concat(files.Images, files.Documents)}

var errAttachmentNotFound = httpx.NotFound("file_not_found")

// ownNote checks that the note of the route is the user's, without reading it.
func (h *Handler) ownNote(r *http.Request) (int64, error) {
	id, err := httpx.PathID(r, "id")
	if err != nil {
		return 0, err
	}
	_, err = h.q.GetNoteID(r.Context(), store.GetNoteIDParams{ID: id, UserID: auth.MustUser(r.Context()).ID})
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, errNotFound
	}
	if err != nil {
		return 0, fmt.Errorf("get note %d: %w", id, err)
	}
	return id, nil
}

// listAttachments answers GET /notes/{id}/attachments.
func (h *Handler) listAttachments(w http.ResponseWriter, r *http.Request) error {
	id, err := h.ownNote(r)
	if err != nil {
		return err
	}
	rows, err := h.q.ListNoteAttachments(r.Context(), id)
	if err != nil {
		return fmt.Errorf("list the attachments of note %d: %w", id, err)
	}
	data := make([]files.File, len(rows))
	for i, f := range rows {
		data[i] = files.ToResponse(f)
	}
	httpx.Write(w, http.StatusOK, map[string][]files.File{"data": data})
	return nil
}

// attach answers POST /notes/{id}/attachments (multipart/form-data, with the file in the "file"
// field): it stores the file and adds it to the note.
func (h *Handler) attach(w http.ResponseWriter, r *http.Request) error {
	// The note first: the body is not read for a note that is not the user's.
	id, err := h.ownNote(r)
	if err != nil {
		return err
	}
	file, err := h.files.Receive(w, r, attachmentRule)
	if err != nil {
		return err
	}

	user := auth.MustUser(r.Context())
	err = pgx.BeginFunc(r.Context(), h.db, func(tx pgx.Tx) error {
		q := h.q.WithTx(tx)
		if err := files.Attach(r.Context(), q, user.ID, file.ID); err != nil {
			return err
		}
		err := q.CreateNoteAttachment(r.Context(), store.CreateNoteAttachmentParams{NoteID: id, FileID: file.ID})
		if _, ok := db.IsForeignKeyViolation(err); ok {
			return errNotFound // the note was deleted while the file was uploading
		}
		if err != nil {
			return fmt.Errorf("attach file %d to note %d: %w", file.ID, id, err)
		}
		return nil
	})
	if err != nil {
		// The file stays without a note: it is released now, so it does not count for the quota
		// until the cleanup finds it a day later. Not with the request's context, which may be why
		// the transaction failed.
		if relErr := files.Release(context.WithoutCancel(r.Context()), h.q, file.ID); relErr != nil {
			httpx.LoggerFrom(r.Context()).Error("could not release a file that was not attached", "file_id", file.ID, "error", relErr)
		}
		return err
	}

	httpx.Write(w, http.StatusCreated, files.ToResponse(file))
	return nil
}

// detach answers DELETE /notes/{id}/attachments/{file_id}: it takes the file off the note and
// deletes it.
func (h *Handler) detach(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.PathID(r, "id")
	if err != nil {
		return err
	}
	fileID, err := httpx.PathID(r, "file_id")
	if err != nil {
		return err
	}

	user := auth.MustUser(r.Context())
	err = pgx.BeginFunc(r.Context(), h.db, func(tx pgx.Tx) error {
		q := h.q.WithTx(tx)
		rows, err := q.DeleteNoteAttachment(r.Context(), store.DeleteNoteAttachmentParams{NoteID: id, FileID: fileID, UserID: user.ID})
		if err != nil {
			return fmt.Errorf("detach file %d from note %d: %w", fileID, id, err)
		}
		if rows == 0 {
			return errAttachmentNotFound // not in this note, or the note is someone else's
		}
		return files.Release(r.Context(), q, fileID)
	})
	if err != nil {
		return err
	}

	httpx.Write(w, http.StatusNoContent, nil)
	return nil
}
