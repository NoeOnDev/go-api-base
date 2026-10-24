// Package products es la segunda mitad del ejemplo de catálogo. Además del patrón de notes,
// muestra cómo:
//   - relacionar tablas: cada producto pertenece a una categoría y la respuesta la incluye (JOIN);
//   - filtrar y buscar en un listado con parámetros opcionales;
//   - traducir errores de la base: slug repetido → 409, categoría inexistente → 422.
package products

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/NoeOnDev/go-api-base/internal/db"
	"github.com/NoeOnDev/go-api-base/internal/httpx"
	"github.com/NoeOnDev/go-api-base/internal/store"
)

// Handler agrupa los endpoints de productos.
type Handler struct {
	q *store.Queries
}

// NewHandler crea el handler a partir de las consultas generadas por sqlc.
func NewHandler(q *store.Queries) *Handler {
	return &Handler{q: q}
}

type categoryRef struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type productResponse struct {
	ID          int64       `json:"id"`
	Category    categoryRef `json:"category"`
	Name        string      `json:"name"`
	Slug        string      `json:"slug"`
	Description string      `json:"description"`
	PriceCents  int64       `json:"price_cents"`
	Stock       int32       `json:"stock"`
	Active      bool        `json:"active"`
	CreatedAt   time.Time   `json:"created_at"`
	UpdatedAt   time.Time   `json:"updated_at"`
}

// toResponse recibe la fila de GetProduct. ListProductsRow tiene los mismos campos,
// así que se convierte con store.GetProductRow(row).
func toResponse(p store.GetProductRow) productResponse {
	return productResponse{
		ID:          p.ID,
		Category:    categoryRef{ID: p.CategoryID, Name: p.CategoryName},
		Name:        p.Name,
		Slug:        p.Slug,
		Description: p.Description,
		PriceCents:  p.PriceCents,
		Stock:       p.Stock,
		Active:      p.Active,
		CreatedAt:   p.CreatedAt,
		UpdatedAt:   p.UpdatedAt,
	}
}

var (
	errNotFound   = httpx.NotFound("el producto no existe")
	errSlugTaken  = httpx.Conflict("ya existe un producto con ese slug", "slug")
	errNoCategory = httpx.Validation(map[string]string{"category_id": "la categoría no existe"})
)

// writeError traduce los errores de la base que dependen de los datos enviados.
func writeError(err error, action string) error {
	if _, ok := db.IsUniqueViolation(err); ok {
		return errSlugTaken
	}
	if _, ok := db.IsForeignKeyViolation(err); ok {
		return errNoCategory
	}
	return fmt.Errorf("%s: %w", action, err)
}

// list responde GET /products?limit=20&offset=0&category_id=3&q=café&active=true.
func (h *Handler) list(w http.ResponseWriter, r *http.Request) error {
	page, err := httpx.ParsePage(r)
	if err != nil {
		return err
	}
	f, err := parseFilters(r)
	if err != nil {
		return err
	}

	rows, err := h.q.ListProducts(r.Context(), store.ListProductsParams{
		CategoryID: f.CategoryID,
		Search:     f.Search,
		Active:     f.Active,
		Limit:      page.Limit,
		Offset:     page.Offset,
	})
	if err != nil {
		return fmt.Errorf("listar productos: %w", err)
	}

	total, err := h.q.CountProducts(r.Context(), store.CountProductsParams{
		CategoryID: f.CategoryID,
		Search:     f.Search,
		Active:     f.Active,
	})
	if err != nil {
		return fmt.Errorf("contar productos: %w", err)
	}

	data := make([]productResponse, len(rows))
	for i, row := range rows {
		data[i] = toResponse(store.GetProductRow(row))
	}

	httpx.Write(w, http.StatusOK, httpx.List[productResponse]{
		Data: data,
		Meta: httpx.PageMeta{Limit: page.Limit, Offset: page.Offset, Total: total},
	})
	return nil
}

// get responde GET /products/{id}.
func (h *Handler) get(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.PathID(r, "id")
	if err != nil {
		return err
	}
	return h.writeProduct(r.Context(), w, http.StatusOK, id)
}

// create responde POST /products.
func (h *Handler) create(w http.ResponseWriter, r *http.Request) error {
	var in productInput
	if err := httpx.Decode(w, r, &in); err != nil {
		return err
	}
	in.normalize()
	if err := in.validate(); err != nil {
		return err
	}

	id, err := h.q.CreateProduct(r.Context(), store.CreateProductParams{
		CategoryID:  in.CategoryID,
		Name:        in.Name,
		Slug:        in.Slug,
		Description: in.Description,
		PriceCents:  in.PriceCents,
		Stock:       in.Stock,
		Active:      *in.Active,
	})
	if err != nil {
		return writeError(err, "crear producto")
	}

	w.Header().Set("Location", fmt.Sprintf("/products/%d", id))
	return h.writeProduct(r.Context(), w, http.StatusCreated, id)
}

// update responde PUT /products/{id}. Reemplaza el producto completo.
func (h *Handler) update(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.PathID(r, "id")
	if err != nil {
		return err
	}

	var in productInput
	if err := httpx.Decode(w, r, &in); err != nil {
		return err
	}
	in.normalize()
	if err := in.validate(); err != nil {
		return err
	}

	_, err = h.q.UpdateProduct(r.Context(), store.UpdateProductParams{
		ID:          id,
		CategoryID:  in.CategoryID,
		Name:        in.Name,
		Slug:        in.Slug,
		Description: in.Description,
		PriceCents:  in.PriceCents,
		Stock:       in.Stock,
		Active:      *in.Active,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return errNotFound
	}
	if err != nil {
		return writeError(err, fmt.Sprintf("actualizar producto %d", id))
	}

	return h.writeProduct(r.Context(), w, http.StatusOK, id)
}

// delete responde DELETE /products/{id}.
func (h *Handler) delete(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.PathID(r, "id")
	if err != nil {
		return err
	}

	rows, err := h.q.DeleteProduct(r.Context(), id)
	if err != nil {
		return fmt.Errorf("borrar producto %d: %w", id, err)
	}
	if rows == 0 {
		return errNotFound
	}

	httpx.Write(w, http.StatusNoContent, nil)
	return nil
}

// writeProduct lee el producto con su categoría y lo responde.
// Crear y editar solo devuelven el id, así que se reutiliza la misma consulta con JOIN.
func (h *Handler) writeProduct(ctx context.Context, w http.ResponseWriter, status int, id int64) error {
	p, err := h.q.GetProduct(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return errNotFound
	}
	if err != nil {
		return fmt.Errorf("obtener producto %d: %w", id, err)
	}

	httpx.Write(w, status, toResponse(p))
	return nil
}
