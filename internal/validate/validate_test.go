package validate

import (
	"strings"
	"testing"
)

func TestSlug(t *testing.T) {
	valid := []string{"cafe", "cafe-molido", "taza-2", "a1-b2-c3"}
	invalid := []string{"", "Café", "cafe molido", "-cafe", "cafe-", "cafe--molido", "cafe_molido", strings.Repeat("a", 121)}

	for _, s := range valid {
		if msg := Slug(s, 120); !msg.IsZero() {
			t.Errorf("Slug(%q) = %q, se esperaba válido", s, msg)
		}
	}
	for _, s := range invalid {
		if msg := Slug(s, 120); msg.IsZero() {
			t.Errorf("Slug(%q) debería ser inválido", s)
		}
	}
}

func TestRequiredCountsCharactersNotBytes(t *testing.T) {
	if msg := Required(strings.Repeat("ñ", 10), 10); !msg.IsZero() {
		t.Errorf("10 caracteres con ñ deberían ser válidos: %q", msg)
	}
	if msg := Required("   ", 10); msg.IsZero() {
		t.Error("solo espacios debería ser inválido")
	}
}
