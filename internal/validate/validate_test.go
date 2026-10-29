package validate

import (
	"strings"
	"testing"
)

func TestSlug(t *testing.T) {
	valid := []string{"cafe", "cafe-au-lait", "cup-2", "a1-b2-c3"}
	invalid := []string{"", "Café", "cafe au lait", "-cafe", "cafe-", "cafe--au-lait", "cafe_au_lait", strings.Repeat("a", 121)}

	for _, s := range valid {
		if msg := Slug(s, 120); !msg.IsZero() {
			t.Errorf("Slug(%q) = %q, want valid", s, msg)
		}
	}
	for _, s := range invalid {
		if msg := Slug(s, 120); msg.IsZero() {
			t.Errorf("Slug(%q) should be invalid", s)
		}
	}
}

func TestRequiredCountsCharactersNotBytes(t *testing.T) {
	if msg := Required(strings.Repeat("ñ", 10), 10); !msg.IsZero() {
		t.Errorf("10 characters with ñ should be valid: %q", msg)
	}
	if msg := Required("   ", 10); msg.IsZero() {
		t.Error("only spaces should be invalid")
	}
}
