// Package notes es el recurso de ejemplo de la plantilla: un CRUD completo que muestra
// el patrón a seguir (consultas con sqlc, validación, errores y tests).
// Para crear un recurso nuevo, copiá este paquete y adaptalo (ver docs/adding-a-resource.md).
//
// También muestra la pertenencia: cada nota es de un usuario, y todas las consultas filtran
// por el usuario de la sesión. Si alguien pide la nota de otro usuario, la consulta no la
// encuentra y la API responde 404 (no 403), para no revelar que esa nota existe.
//
// Y el tiempo real: cada cambio se publica en el canal del dueño (note.created, note.updated,
// note.deleted), así otra pestaña u otro dispositivo se actualiza sin recargar. Para datos
// compartidos (un tablero, un proyecto) se publica en un tema; ver docs/realtime.md.
package notes

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/NoeOnDev/go-api-base/internal/auth"
	"github.com/NoeOnDev/go-api-base/internal/httpx"
	"github.com/NoeOnDev/go-api-base/internal/realtime"
	"github.com/NoeOnDev/go-api-base/internal/store"
)

// Handler agrupa los endpoints de notas y sus dependencias.
type Handler struct {
	q      *store.Queries
	broker *realtime.Broker
}

// NewHandler crea el handler a partir de las consultas generadas por sqlc y el broker
// de eventos en tiempo real.
func NewHandler(q *store.Queries, broker *realtime.Broker) *Handler {
	return &Handler{q: q, broker: broker}
}

// Eventos que se publican en el canal del dueño de la nota.
const (
	EventCreated = "note.created" // con la nota
	EventUpdated = "note.updated" // con la nota
	EventDeleted = "note.deleted" // con {"id": …}
)

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

var errNotFound = httpx.NotFound("note_not_found")

// list responde GET /notes?limit=20&offset=0.
func (h *Handler) list(w http.ResponseWriter, r *http.Request) error {
	page, err := httpx.ParsePage(r)
	if err != nil {
		return err
	}

	user := auth.MustUser(r.Context())

	notes, err := h.q.ListNotes(r.Context(), store.ListNotesParams{UserID: user.ID, Limit: page.Limit, Offset: page.Offset})
	if err != nil {
		return fmt.Errorf("listar notas: %w", err)
	}

	total, err := h.q.CountNotes(r.Context(), user.ID)
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

	note, err := h.q.GetNote(r.Context(), store.GetNoteParams{ID: id, UserID: auth.MustUser(r.Context()).ID})
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

	note, err := h.q.CreateNote(r.Context(), store.CreateNoteParams{
		UserID: auth.MustUser(r.Context()).ID,
		Title:  in.Title,
		Body:   in.Body,
	})
	if err != nil {
		return fmt.Errorf("crear nota: %w", err)
	}

	h.broker.PublishToUser(r.Context(), note.UserID, realtime.Event{Type: EventCreated, Data: toResponse(note)})

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

	note, err := h.q.UpdateNote(r.Context(), store.UpdateNoteParams{
		ID:     id,
		UserID: auth.MustUser(r.Context()).ID,
		Title:  in.Title,
		Body:   in.Body,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return errNotFound
	}
	if err != nil {
		return fmt.Errorf("actualizar nota %d: %w", id, err)
	}

	h.broker.PublishToUser(r.Context(), note.UserID, realtime.Event{Type: EventUpdated, Data: toResponse(note)})
	httpx.Write(w, http.StatusOK, toResponse(note))
	return nil
}

// delete responde DELETE /notes/{id}.
func (h *Handler) delete(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.PathID(r, "id")
	if err != nil {
		return err
	}

	user := auth.MustUser(r.Context())
	rows, err := h.q.DeleteNote(r.Context(), store.DeleteNoteParams{ID: id, UserID: user.ID})
	if err != nil {
		return fmt.Errorf("borrar nota %d: %w", id, err)
	}
	if rows == 0 {
		return errNotFound
	}

	h.broker.PublishToUser(r.Context(), user.ID, realtime.Event{Type: EventDeleted, Data: map[string]int64{"id": id}})

	httpx.Write(w, http.StatusNoContent, nil)
	return nil
}
