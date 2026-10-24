// Package categories es la primera mitad del ejemplo de catálogo: lectura pública,
// escritura solo para administradores, un valor único (slug) que responde 409 si se repite
// y una relación con products que impide borrar una categoría en uso.
package categories

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/NoeOnDev/go-api-base/internal/db"
	"github.com/NoeOnDev/go-api-base/internal/httpx"
	"github.com/NoeOnDev/go-api-base/internal/store"
)

// Handler agrupa los endpoints de categorías.
type Handler struct {
	q *store.Queries
}

// NewHandler crea el handler a partir de las consultas generadas por sqlc.
func NewHandler(q *store.Queries) *Handler {
	return &Handler{q: q}
}

type categoryResponse struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	Slug      string    `json:"slug"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func toResponse(c store.Category) categoryResponse {
	return categoryResponse{ID: c.ID, Name: c.Name, Slug: c.Slug, CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt}
}

var (
	errNotFound  = httpx.NotFound("la categoría no existe")
	errSlugTaken = httpx.Conflict("ya existe una categoría con ese slug", "slug")
	errInUse     = httpx.Conflict("la categoría tiene productos: borralos o cambialos de categoría antes", "")
)

// list responde GET /categories?limit=20&offset=0, ordenadas por nombre.
func (h *Handler) list(w http.ResponseWriter, r *http.Request) error {
	page, err := httpx.ParsePage(r)
	if err != nil {
		return err
	}

	categories, err := h.q.ListCategories(r.Context(), store.ListCategoriesParams{Limit: page.Limit, Offset: page.Offset})
	if err != nil {
		return fmt.Errorf("listar categorías: %w", err)
	}
	total, err := h.q.CountCategories(r.Context())
	if err != nil {
		return fmt.Errorf("contar categorías: %w", err)
	}

	data := make([]categoryResponse, len(categories))
	for i, c := range categories {
		data[i] = toResponse(c)
	}

	httpx.Write(w, http.StatusOK, httpx.List[categoryResponse]{
		Data: data,
		Meta: httpx.PageMeta{Limit: page.Limit, Offset: page.Offset, Total: total},
	})
	return nil
}

// get responde GET /categories/{id}.
func (h *Handler) get(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.PathID(r, "id")
	if err != nil {
		return err
	}

	c, err := h.q.GetCategory(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		return errNotFound
	}
	if err != nil {
		return fmt.Errorf("obtener categoría %d: %w", id, err)
	}

	httpx.Write(w, http.StatusOK, toResponse(c))
	return nil
}

// create responde POST /categories.
func (h *Handler) create(w http.ResponseWriter, r *http.Request) error {
	var in categoryInput
	if err := httpx.Decode(w, r, &in); err != nil {
		return err
	}
	in.normalize()
	if err := in.validate(); err != nil {
		return err
	}

	c, err := h.q.CreateCategory(r.Context(), store.CreateCategoryParams{Name: in.Name, Slug: in.Slug})
	if _, ok := db.IsUniqueViolation(err); ok {
		return errSlugTaken
	}
	if err != nil {
		return fmt.Errorf("crear categoría: %w", err)
	}

	w.Header().Set("Location", fmt.Sprintf("/categories/%d", c.ID))
	httpx.Write(w, http.StatusCreated, toResponse(c))
	return nil
}

// update responde PUT /categories/{id}.
func (h *Handler) update(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.PathID(r, "id")
	if err != nil {
		return err
	}

	var in categoryInput
	if err := httpx.Decode(w, r, &in); err != nil {
		return err
	}
	in.normalize()
	if err := in.validate(); err != nil {
		return err
	}

	c, err := h.q.UpdateCategory(r.Context(), store.UpdateCategoryParams{ID: id, Name: in.Name, Slug: in.Slug})
	if errors.Is(err, pgx.ErrNoRows) {
		return errNotFound
	}
	if _, ok := db.IsUniqueViolation(err); ok {
		return errSlugTaken
	}
	if err != nil {
		return fmt.Errorf("actualizar categoría %d: %w", id, err)
	}

	httpx.Write(w, http.StatusOK, toResponse(c))
	return nil
}

// delete responde DELETE /categories/{id}. Si la categoría tiene productos responde 409:
// la base lo impide con la restricción ON DELETE RESTRICT de products.category_id.
func (h *Handler) delete(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.PathID(r, "id")
	if err != nil {
		return err
	}

	rows, err := h.q.DeleteCategory(r.Context(), id)
	if _, ok := db.IsForeignKeyViolation(err); ok {
		return errInUse
	}
	if err != nil {
		return fmt.Errorf("borrar categoría %d: %w", id, err)
	}
	if rows == 0 {
		return errNotFound
	}

	httpx.Write(w, http.StatusNoContent, nil)
	return nil
}
