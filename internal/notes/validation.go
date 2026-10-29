package notes

import (
	"strings"
	"unicode/utf8"

	"github.com/NoeOnDev/go-api-base/internal/httpx"
	"github.com/NoeOnDev/go-api-base/internal/i18n"
)

// Límites de longitud, en caracteres.
const (
	maxTitleLen = 200
	maxBodyLen  = 10000
)

// noteInput es el cuerpo que se acepta al crear o editar una nota.
type noteInput struct {
	Title string `json:"title"`
	Body  string `json:"body"`
}

// normalize limpia los datos antes de validarlos.
func (in *noteInput) normalize() {
	in.Title = strings.TrimSpace(in.Title)
}

// validate revisa las reglas y devuelve un error de validación con todos los campos que fallan.
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
