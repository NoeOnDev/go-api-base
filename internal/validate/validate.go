// Package validate reúne reglas de validación que comparten varios recursos.
// Cada función devuelve el mensaje de error para el campo, o "" si el valor es válido.
package validate

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

// Required exige un texto no vacío de hasta max caracteres.
func Required(value string, max int) string {
	switch {
	case strings.TrimSpace(value) == "":
		return "es obligatorio"
	case utf8.RuneCountInString(value) > max:
		return fmt.Sprintf("no puede superar %d caracteres", max)
	}
	return ""
}

// MaxLen limita un texto opcional a max caracteres.
func MaxLen(value string, max int) string {
	if utf8.RuneCountInString(value) > max {
		return fmt.Sprintf("no puede superar %d caracteres", max)
	}
	return ""
}

var slugPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

// Slug exige un identificador para URLs: minúsculas, números y guiones simples,
// por ejemplo "cafe-de-especialidad".
func Slug(value string, max int) string {
	if msg := Required(value, max); msg != "" {
		return msg
	}
	if !slugPattern.MatchString(value) {
		return "solo puede tener minúsculas sin acentos, números y guiones (ejemplo: cafe-molido)"
	}
	return ""
}

// NonNegative exige un número mayor o igual a cero.
func NonNegative[T int32 | int64](value T) string {
	if value < 0 {
		return "no puede ser negativo"
	}
	return ""
}
