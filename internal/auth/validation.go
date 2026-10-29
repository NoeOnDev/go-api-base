package auth

import (
	"net/mail"
	"strings"
	"unicode/utf8"

	"github.com/NoeOnDev/go-api-base/internal/httpx"
	"github.com/NoeOnDev/go-api-base/internal/i18n"
)

// Límites de longitud, en caracteres.
const (
	maxNameLen     = 100
	maxEmailLen    = 254
	minPasswordLen = 8
	maxPasswordLen = 128
)

// RegisterInput son los datos para crear un usuario.
type RegisterInput struct {
	Name     string `json:"name"`
	Email    string `json:"email"`
	Password string `json:"password"`
	// Locale es el idioma del usuario (ver User.Locale). No llega en el cuerpo: el registro lo
	// toma de Accept-Language, y nil deja el idioma por defecto.
	Locale *string `json:"-"`
}

func (in *RegisterInput) normalize() {
	in.Name = strings.TrimSpace(in.Name)
	in.Email = strings.ToLower(strings.TrimSpace(in.Email))
}

func (in RegisterInput) validate() error {
	fields := httpx.Fields{}

	switch {
	case in.Name == "":
		fields["name"] = i18n.M("validation.required")
	case utf8.RuneCountInString(in.Name) > maxNameLen:
		fields["name"] = i18n.M("validation.too_long", "max", maxNameLen)
	}

	if msg := validateEmail(in.Email); !msg.IsZero() {
		fields["email"] = msg
	}

	if msg := validatePassword(in.Password); !msg.IsZero() {
		fields["password"] = msg
	}

	if len(fields) > 0 {
		return httpx.Validation(fields)
	}
	return nil
}

// loginInput son los datos del login. Solo se comprueba que vengan: la validez real
// la decide la comparación con la base.
type loginInput struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func (in loginInput) validate() error {
	fields := httpx.Fields{}
	switch {
	case strings.TrimSpace(in.Email) == "":
		fields["email"] = i18n.M("validation.required")
	case len(in.Email) > maxEmailLen:
		// El email es la clave del límite por cuenta: sin tope, cada intento podría ocupar
		// hasta el tamaño máximo del cuerpo (1 MB) en memoria.
		fields["email"] = i18n.M("validation.too_long", "max", maxEmailLen)
	}
	if in.Password == "" {
		fields["password"] = i18n.M("validation.required")
	}
	if len(fields) > 0 {
		return httpx.Validation(fields)
	}
	return nil
}

// validatePassword aplica las reglas de una contraseña nueva. No se recorta:
// los espacios también cuentan.
func validatePassword(password string) i18n.Message {
	switch n := utf8.RuneCountInString(password); {
	case n < minPasswordLen:
		return i18n.M("validation.too_short", "min", minPasswordLen)
	case n > maxPasswordLen:
		return i18n.M("validation.too_long", "max", maxPasswordLen)
	}
	return i18n.Message{}
}

func validateEmail(email string) i18n.Message {
	if email == "" {
		return i18n.M("validation.required")
	}
	if len(email) > maxEmailLen {
		return i18n.M("validation.too_long", "max", maxEmailLen)
	}
	// net/mail acepta formas como "Ana <ana@x.com>"; se exige solo la dirección.
	addr, err := mail.ParseAddress(email)
	if err != nil || addr.Address != email || !strings.Contains(email[strings.LastIndex(email, "@"):], ".") {
		return i18n.M("validation.invalid_email")
	}
	return i18n.Message{}
}
