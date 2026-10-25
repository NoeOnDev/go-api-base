package auth

import (
	"net/mail"
	"strings"
	"unicode/utf8"

	"github.com/NoeOnDev/go-api-base/internal/httpx"
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
}

func (in *RegisterInput) normalize() {
	in.Name = strings.TrimSpace(in.Name)
	in.Email = strings.ToLower(strings.TrimSpace(in.Email))
}

func (in RegisterInput) validate() error {
	fields := map[string]string{}

	switch {
	case in.Name == "":
		fields["name"] = "es obligatorio"
	case utf8.RuneCountInString(in.Name) > maxNameLen:
		fields["name"] = "no puede superar 100 caracteres"
	}

	if msg := validateEmail(in.Email); msg != "" {
		fields["email"] = msg
	}

	if msg := validatePassword(in.Password); msg != "" {
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
	fields := map[string]string{}
	switch {
	case strings.TrimSpace(in.Email) == "":
		fields["email"] = "es obligatorio"
	case len(in.Email) > maxEmailLen:
		// El email es la clave del límite por cuenta: sin tope, cada intento podría ocupar
		// hasta el tamaño máximo del cuerpo (1 MB) en memoria.
		fields["email"] = "es demasiado largo"
	}
	if in.Password == "" {
		fields["password"] = "es obligatoria"
	}
	if len(fields) > 0 {
		return httpx.Validation(fields)
	}
	return nil
}

// validatePassword aplica las reglas de una contraseña nueva. No se recorta:
// los espacios también cuentan.
func validatePassword(password string) string {
	switch n := utf8.RuneCountInString(password); {
	case n < minPasswordLen:
		return "debe tener al menos 8 caracteres"
	case n > maxPasswordLen:
		return "no puede superar 128 caracteres"
	}
	return ""
}

func validateEmail(email string) string {
	if email == "" {
		return "es obligatorio"
	}
	if len(email) > maxEmailLen {
		return "es demasiado largo"
	}
	// net/mail acepta formas como "Ana <ana@x.com>"; se exige solo la dirección.
	addr, err := mail.ParseAddress(email)
	if err != nil || addr.Address != email || !strings.Contains(email[strings.LastIndex(email, "@"):], ".") {
		return "no es un email válido"
	}
	return ""
}
