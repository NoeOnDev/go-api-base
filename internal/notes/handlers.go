// Package notes es el recurso de ejemplo de la plantilla: un CRUD completo que muestra
// el patrón a seguir (consultas con sqlc, validación, errores y tests).
// Para crear un recurso nuevo, copiá este paquete y adaptalo (ver docs/adding-a-resource.md).
package notes

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/NoeOnDev/go-api-base/internal/httpx"
	"github.com/NoeOnDev/go-api-base/internal/store"
)

// Handler agrupa los endpoints de notas y sus dependencias.
type Handler struct {
	q *store.Queries
}

// NewHandler crea el handler a partir de las consultas generadas por sqlc.
func NewHandler(q *store.Queries) *Handler {
	return &Handler{q: q}
}

// noteResponse es lo que la API devuelve. Se separa de store.Note para que un cambio
// en la tabla no cambie el contrato con el frontend sin que lo decidas.
type noteResponse struct {
	ID        int64     `json:"id"`
	Title     string    `json:"title"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func toResponse(n store.Note) noteResponse {
	return noteResponse{
		ID:        n.ID,
		Title:     n.Title,
		Body:      n.Body,
		CreatedAt: n.CreatedAt,
		UpdatedAt: n.UpdatedAt,
	}
}

var errNotFound = httpx.NotFound("la nota no existe")

// list responde GET /notes?limit=20&offset=0.
func (h *Handler) list(w http.ResponseWriter, r *http.Request) error {
	page, err := httpx.ParsePage(r)
	if err != nil {
		return err
	}

	notes, err := h.q.ListNotes(r.Context(), store.ListNotesParams{Limit: page.Limit, Offset: page.Offset})
	if err != nil {
		return fmt.Errorf("listar notas: %w", err)
	}

	total, err := h.q.CountNotes(r.Context())
	if err != nil {
		return fmt.Errorf("contar notas: %w", err)
	}

	data := make([]noteResponse, len(notes))
	for i, n := range notes {
		data[i] = toResponse(n)
	}

	httpx.Write(w, http.StatusOK, httpx.List[noteResponse]{
		Data: data,
		Meta: httpx.PageMeta{Limit: page.Limit, Offset: page.Offset, Total: total},
	})
	return nil
}

// get responde GET /notes/{id}.
func (h *Handler) get(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.PathID(r, "id")
	if err != nil {
		return err
	}

	note, err := h.q.GetNote(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		return errNotFound
	}
	if err != nil {
		return fmt.Errorf("obtener nota %d: %w", id, err)
	}

	httpx.Write(w, http.StatusOK, toResponse(note))
	return nil
}

// create responde POST /notes.
func (h *Handler) create(w http.ResponseWriter, r *http.Request) error {
	var in noteInput
	if err := httpx.Decode(w, r, &in); err != nil {
		return err
	}
	in.normalize()
	if err := in.validate(); err != nil {
		return err
	}

	note, err := h.q.CreateNote(r.Context(), store.CreateNoteParams{Title: in.Title, Body: in.Body})
	if err != nil {
		return fmt.Errorf("crear nota: %w", err)
	}

	w.Header().Set("Location", fmt.Sprintf("/notes/%d", note.ID))
	httpx.Write(w, http.StatusCreated, toResponse(note))
	return nil
}

// update responde PUT /notes/{id}. Reemplaza la nota completa.
func (h *Handler) update(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.PathID(r, "id")
	if err != nil {
		return err
	}

	var in noteInput
	if err := httpx.Decode(w, r, &in); err != nil {
		return err
	}
	in.normalize()
	if err := in.validate(); err != nil {
		return err
	}

	note, err := h.q.UpdateNote(r.Context(), store.UpdateNoteParams{ID: id, Title: in.Title, Body: in.Body})
	if errors.Is(err, pgx.ErrNoRows) {
		return errNotFound
	}
	if err != nil {
		return fmt.Errorf("actualizar nota %d: %w", id, err)
	}

	httpx.Write(w, http.StatusOK, toResponse(note))
	return nil
}

// delete responde DELETE /notes/{id}.
func (h *Handler) delete(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.PathID(r, "id")
	if err != nil {
		return err
	}

	rows, err := h.q.DeleteNote(r.Context(), id)
	if err != nil {
		return fmt.Errorf("borrar nota %d: %w", id, err)
	}
	if rows == 0 {
		return errNotFound
	}

	httpx.Write(w, http.StatusNoContent, nil)
	return nil
}
