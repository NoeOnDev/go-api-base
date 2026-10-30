// Package validate gathers validation rules shared by several resources.
// Each function returns the error message for the field (to be translated; see internal/i18n),
// or an empty message if the value is valid: if msg := validate.Required(v, 100); !msg.IsZero() {…}.
package validate

import (
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/NoeOnDev/go-api-base/internal/i18n"
)

// Required demands a non-empty text of up to max characters.
func Required(value string, max int) i18n.Message {
	switch {
	case strings.TrimSpace(value) == "":
		return i18n.M("validation.required")
	case utf8.RuneCountInString(value) > max:
		return i18n.M("validation.too_long", "max", max)
	}
	return i18n.Message{}
}

// MaxLen limits an optional text to max characters.
func MaxLen(value string, max int) i18n.Message {
	if utf8.RuneCountInString(value) > max {
		return i18n.M("validation.too_long", "max", max)
	}
	return i18n.Message{}
}

var slugPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

// Slug demands an identifier for URLs: lowercase letters, numbers and single hyphens,
// for example "specialty-coffee".
func Slug(value string, max int) i18n.Message {
	if msg := Required(value, max); !msg.IsZero() {
		return msg
	}
	if !slugPattern.MatchString(value) {
		return i18n.M("validation.invalid_slug")
	}
	return i18n.Message{}
}

// NonNegative demands a number greater than or equal to zero.
func NonNegative[T int32 | int64](value T) i18n.Message {
	if value < 0 {
		return i18n.M("validation.negative")
	}
	return i18n.Message{}
}
