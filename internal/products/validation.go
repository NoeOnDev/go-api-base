package products

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/NoeOnDev/go-api-base/internal/db"
	"github.com/NoeOnDev/go-api-base/internal/httpx"
	"github.com/NoeOnDev/go-api-base/internal/i18n"
	"github.com/NoeOnDev/go-api-base/internal/validate"
)

// productInput is the body to create or replace a product.
type productInput struct {
	CategoryID  int64  `json:"category_id"`
	Name        string `json:"name"`
	Slug        string `json:"slug"`
	Description string `json:"description"`
	PriceCents  int64  `json:"price_cents"`
	Stock       int32  `json:"stock"`
	// Active is a pointer to tell "not sent" (true is used) apart from false.
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
	fields := httpx.Fields{}

	if in.CategoryID < 1 {
		fields["category_id"] = i18n.M("validation.required")
	}
	if msg := validate.Required(in.Name, 120); !msg.IsZero() {
		fields["name"] = msg
	}
	if msg := validate.Slug(in.Slug, 120); !msg.IsZero() {
		fields["slug"] = msg
	}
	if msg := validate.MaxLen(in.Description, 5000); !msg.IsZero() {
		fields["description"] = msg
	}
	if msg := validate.NonNegative(in.PriceCents); !msg.IsZero() {
		fields["price_cents"] = msg
	}
	if msg := validate.NonNegative(in.Stock); !msg.IsZero() {
		fields["stock"] = msg
	}

	if len(fields) > 0 {
		return httpx.Validation(fields)
	}
	return nil
}

// filters are the optional filters of the list: ?category_id=3&q=café&active=true.
// A nil field means "no filter".
type filters struct {
	CategoryID *int64
	Search     *string
	Active     *bool
}

func parseFilters(r *http.Request) (filters, error) {
	var f filters
	fields := httpx.Fields{}
	query := r.URL.Query()

	if v := query.Get("category_id"); v != "" {
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil || id < 1 {
			fields["category_id"] = i18n.M("validation.positive_integer")
		}
		f.CategoryID = &id
	}

	if v := strings.TrimSpace(query.Get("q")); v != "" {
		if msg := validate.MaxLen(v, 100); !msg.IsZero() {
			fields["q"] = msg
		}
		escaped := db.EscapeLike(v)
		f.Search = &escaped
	}

	if v := query.Get("active"); v != "" {
		active, err := strconv.ParseBool(v)
		if err != nil {
			fields["active"] = i18n.M("validation.boolean")
		}
		f.Active = &active
	}

	if len(fields) > 0 {
		return filters{}, httpx.Validation(fields)
	}
	return f, nil
}
