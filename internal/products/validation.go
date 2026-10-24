package products

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/NoeOnDev/go-api-base/internal/httpx"
	"github.com/NoeOnDev/go-api-base/internal/validate"
)

// productInput es el cuerpo para crear o reemplazar un producto.
type productInput struct {
	CategoryID  int64  `json:"category_id"`
	Name        string `json:"name"`
	Slug        string `json:"slug"`
	Description string `json:"description"`
	PriceCents  int64  `json:"price_cents"`
	Stock       int32  `json:"stock"`
	// Active es un puntero para distinguir "no enviado" (se usa true) de false.
	Active *bool `json:"active"`
}

func (in *productInput) normalize() {
	in.Name = strings.TrimSpace(in.Name)
	in.Slug = strings.TrimSpace(in.Slug)
	in.Description = strings.TrimSpace(in.Description)
	if in.Active == nil {
		active := true
		in.Active = &active
	}
}

func (in productInput) validate() error {
	fields := map[string]string{}

	if in.CategoryID < 1 {
		fields["category_id"] = "es obligatorio"
	}
	if msg := validate.Required(in.Name, 120); msg != "" {
		fields["name"] = msg
	}
	if msg := validate.Slug(in.Slug, 120); msg != "" {
		fields["slug"] = msg
	}
	if msg := validate.MaxLen(in.Description, 5000); msg != "" {
		fields["description"] = msg
	}
	if msg := validate.NonNegative(in.PriceCents); msg != "" {
		fields["price_cents"] = msg
	}
	if msg := validate.NonNegative(in.Stock); msg != "" {
		fields["stock"] = msg
	}

	if len(fields) > 0 {
		return httpx.Validation(fields)
	}
	return nil
}

// filters son los filtros opcionales del listado: ?category_id=3&q=café&active=true.
// Un campo nil significa "sin filtro".
type filters struct {
	CategoryID *int64
	Search     *string
	Active     *bool
}

func parseFilters(r *http.Request) (filters, error) {
	var f filters
	fields := map[string]string{}
	query := r.URL.Query()

	if v := query.Get("category_id"); v != "" {
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil || id < 1 {
			fields["category_id"] = "debe ser un número entero positivo"
		}
		f.CategoryID = &id
	}

	if v := strings.TrimSpace(query.Get("q")); v != "" {
		if msg := validate.MaxLen(v, 100); msg != "" {
			fields["q"] = msg
		}
		escaped := escapeLike(v)
		f.Search = &escaped
	}

	if v := query.Get("active"); v != "" {
		active, err := strconv.ParseBool(v)
		if err != nil {
			fields["active"] = "debe ser true o false"
		}
		f.Active = &active
	}

	if len(fields) > 0 {
		return filters{}, httpx.Validation(fields)
	}
	return f, nil
}

// escapeLike escapa los comodines de ILIKE (%, _ y \) para que se busquen como texto literal:
// buscar "50%" debe encontrar "Descuento 50%", no cualquier cosa que empiece con "50".
var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

func escapeLike(s string) string {
	return likeEscaper.Replace(s)
}
