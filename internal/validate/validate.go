// Package validate reúne reglas de validación que comparten varios recursos.
// Cada función devuelve el mensaje de error para el campo (por traducir; ver internal/i18n), o un
// mensaje vacío si el valor es válido: if msg := validate.Required(v, 100); !msg.IsZero() {…}.
package validate

import (
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/NoeOnDev/go-api-base/internal/i18n"
)

// Required exige un texto no vacío de hasta max caracteres.
func Required(value string, max int) i18n.Message {
	switch {
	case strings.TrimSpace(value) == "":
		return i18n.M("validation.required")
	case utf8.RuneCountInString(value) > max:
		return i18n.M("validation.too_long", "max", max)
	}
	return i18n.Message{}
}

// MaxLen limita un texto opcional a max caracteres.
func MaxLen(value string, max int) i18n.Message {
	if utf8.RuneCountInString(value) > max {
		return i18n.M("validation.too_long", "max", max)
	}
	return i18n.Message{}
}

var slugPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

// Slug exige un identificador para URLs: minúsculas, números y guiones simples,
// por ejemplo "cafe-de-especialidad".
func Slug(value string, max int) i18n.Message {
	if msg := Required(value, max); !msg.IsZero() {
		return msg
	}
	if !slugPattern.MatchString(value) {
		return i18n.M("validation.invalid_slug")
	}
	return i18n.Message{}
}

// NonNegative exige un número mayor o igual a cero.
func NonNegative[T int32 | int64](value T) i18n.Message {
	if value < 0 {
		return i18n.M("validation.negative")
	}
	return i18n.Message{}
}
