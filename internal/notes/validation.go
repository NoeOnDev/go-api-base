package notes

import (
	"strings"
	"unicode/utf8"

	"github.com/NoeOnDev/go-api-base/internal/httpx"
	"github.com/NoeOnDev/go-api-base/internal/i18n"
)

// Length limits, in characters.
const (
	maxTitleLen = 200
	maxBodyLen  = 10000
)

// noteInput is the body accepted when creating or editing a note.
type noteInput struct {
	Title string `json:"title"`
	Body  string `json:"body"`
}

// normalize cleans the data before validating it.
func (in *noteInput) normalize() {
	in.Title = strings.TrimSpace(in.Title)
}

// validate checks the rules and returns a validation error with every field that fails.
func (in noteInput) validate() error {
	fields := httpx.Fields{}

	switch {
	case in.Title == "":
		fields["title"] = i18n.M("validation.required")
	case utf8.RuneCountInString(in.Title) > maxTitleLen:
		fields["title"] = i18n.M("validation.too_long", "max", maxTitleLen)
	}

	if utf8.RuneCountInString(in.Body) > maxBodyLen {
		fields["body"] = i18n.M("validation.too_long", "max", maxBodyLen)
	}

	if len(fields) > 0 {
		return httpx.Validation(fields)
	}
	return nil
}
