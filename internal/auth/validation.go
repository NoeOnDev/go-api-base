package auth

import (
	"net/mail"
	"strings"
	"unicode/utf8"

	"github.com/NoeOnDev/go-api-base/internal/httpx"
	"github.com/NoeOnDev/go-api-base/internal/i18n"
)

// Length limits, in characters.
const (
	maxNameLen     = 100
	maxEmailLen    = 254
	minPasswordLen = 8
	maxPasswordLen = 128
)

// RegisterInput is the data to create a user.
type RegisterInput struct {
	Name     string `json:"name"`
	Email    string `json:"email"`
	Password string `json:"password"`
	// Locale is the user's language (see User.Locale). It does not come in the body: registration
	// takes it from Accept-Language, and nil leaves the default language.
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

// loginInput is the login data. It only checks that it is there: the database comparison
// decides whether it is valid.
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
		// The email is the key of the per-account limit: without a cap, each attempt could take up to
		// the maximum body size (1 MB) in memory.
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

// validatePassword applies the rules of a new password: its length, and that it is not one of
// the most common ones (see isCommonPassword). It is not trimmed: spaces count too.
func validatePassword(password string) i18n.Message {
	switch n := utf8.RuneCountInString(password); {
	case n < minPasswordLen:
		return i18n.M("validation.too_short", "min", minPasswordLen)
	case n > maxPasswordLen:
		return i18n.M("validation.too_long", "max", maxPasswordLen)
	case isCommonPassword(password):
		return i18n.M("validation.common_password")
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
	// net/mail accepts forms like "Alice <alice@x.com>"; only the address is accepted.
	addr, err := mail.ParseAddress(email)
	if err != nil || addr.Address != email || !strings.Contains(email[strings.LastIndex(email, "@"):], ".") {
		return i18n.M("validation.invalid_email")
	}
	return i18n.Message{}
}
