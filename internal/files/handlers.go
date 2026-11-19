package files

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/jackc/pgx/v5"

	"github.com/NoeOnDev/go-api-base/internal/auth"
	"github.com/NoeOnDev/go-api-base/internal/httpx"
	"github.com/NoeOnDev/go-api-base/internal/store"
)

// upload answers POST /files (multipart/form-data, with the file in the "file" field): it stores
// the file and returns it. It is for a form that picks the file before the resource exists: the
// form then sends the id, and the resource takes it with Attach. A resource that already exists can
// have its own upload route with Receive (see internal/notes/attachments.go).
func (s *Service) upload(w http.ResponseWriter, r *http.Request) error {
	file, err := s.Receive(w, r, DefaultRule)
	if err != nil {
		return err
	}
	resp := ToResponse(file)
	w.Header().Set("Location", resp.URL)
	httpx.Write(w, http.StatusCreated, resp)
	return nil
}

// download answers GET /files/{id}: the file, for its owner or an administrator. Someone else's
// answers 404, like the other resources with an owner.
func (s *Service) download(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.PathID(r, "id")
	if err != nil {
		return err
	}
	file, err := s.q.GetFile(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		return errNotFound
	}
	if err != nil {
		return fmt.Errorf("get file %d: %w", id, err)
	}
	if user := auth.MustUser(r.Context()); !user.IsAdmin() && (file.UserID == nil || *file.UserID != user.ID) {
		return errNotFound
	}
	return s.serve(w, r, file)
}

// remove answers DELETE /files/{id}: it deletes a file of the user that no resource uses (the form
// was not sent, or another file was picked). One in use answers 409: it is taken off its resource.
func (s *Service) remove(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.PathID(r, "id")
	if err != nil {
		return err
	}
	user := auth.MustUser(r.Context())
	rows, err := s.q.ReleaseUnattachedFile(r.Context(), store.ReleaseUnattachedFileParams{ID: id, UserID: &user.ID})
	if err != nil {
		return fmt.Errorf("release file %d: %w", id, err)
	}
	if rows == 0 {
		file, err := s.q.GetFile(r.Context(), id)
		if err == nil && file.UserID != nil && *file.UserID == user.ID {
			return errInUse
		}
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("get file %d: %w", id, err)
		}
		return errNotFound
	}
	httpx.Write(w, http.StatusNoContent, nil)
	return nil
}
