package categories

import (
	"strings"

	"github.com/NoeOnDev/go-api-base/internal/httpx"
	"github.com/NoeOnDev/go-api-base/internal/validate"
)

type categoryInput struct {
	Name string `json:"name"`
	Slug string `json:"slug"`
}

func (in *categoryInput) normalize() {
	in.Name = strings.TrimSpace(in.Name)
	in.Slug = strings.TrimSpace(in.Slug)
}

func (in categoryInput) validate() error {
	fields := httpx.Fields{}
	if msg := validate.Required(in.Name, 100); !msg.IsZero() {
		fields["name"] = msg
	}
	if msg := validate.Slug(in.Slug, 100); !msg.IsZero() {
		fields["slug"] = msg
	}
	if len(fields) > 0 {
		return httpx.Validation(fields)
	}
	return nil
}
