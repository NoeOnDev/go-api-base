package httpx

import (
	"net/http"
	"strconv"

	"github.com/NoeOnDev/go-api-base/internal/i18n"
)

// Valores de paginación por defecto para los listados.
const (
	DefaultLimit = 20
	MaxLimit     = 100
	// MaxOffset es el offset más alto que se acepta. PostgreSQL lee y descarta todas las filas
	// anteriores al offset, así que uno enorme hace lenta la consulta. Para recorrer más que esto
	// (una exportación, un scroll infinito largo), conviene paginar por cursor: WHERE id < $último.
	MaxOffset = 10_000
)

// Page son los parámetros de paginación de un listado (?limit=20&offset=40).
type Page struct {
	Limit  int32
	Offset int32
}

// PageMeta acompaña a cada listado para que el frontend pueda armar la paginación.
type PageMeta struct {
	Limit  int32 `json:"limit"`
	Offset int32 `json:"offset"`
	Total  int64 `json:"total"`
}

// List es la respuesta de un listado: {"data": [...], "meta": {...}}.
type List[T any] struct {
	Data []T      `json:"data"`
	Meta PageMeta `json:"meta"`
}

// ParsePage lee limit y offset de la URL, con valores por defecto.
// Si son inválidos devuelve un error de validación con el campo correspondiente.
func ParsePage(r *http.Request) (Page, error) {
	page := Page{Limit: DefaultLimit}
	fields := Fields{}

	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.ParseInt(v, 10, 32)
		if err != nil || n < 1 || n > MaxLimit {
			fields["limit"] = i18n.M("validation.integer_range", "min", 1, "max", MaxLimit)
		}
		page.Limit = int32(n)
	}

	if v := r.URL.Query().Get("offset"); v != "" {
		n, err := strconv.ParseInt(v, 10, 32)
		if err != nil || n < 0 || n > MaxOffset {
			fields["offset"] = i18n.M("validation.integer_range", "min", 0, "max", MaxOffset)
		}
		page.Offset = int32(n)
	}

	if len(fields) > 0 {
		return Page{}, Validation(fields)
	}
	return page, nil
}

// PathID lee un identificador numérico de la ruta, por ejemplo {id} en "GET /notes/{id}".
func PathID(r *http.Request, name string) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue(name), 10, 64)
	if err != nil || id < 1 {
		return 0, BadRequest("invalid_path_id", "name", name)
	}
	return id, nil
}
