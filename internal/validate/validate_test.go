package validate

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/NoeOnDev/go-api-base/internal/civil"
	"github.com/NoeOnDev/go-api-base/internal/i18n"
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

func TestCivil(t *testing.T) {
	var in struct {
		On    civil.Date     `json:"on"`
		At    civil.Time     `json:"at"`
		When  civil.DateTime `json:"when"`
		Empty civil.Date     `json:"empty"`
	}
	if err := json.Unmarshal([]byte(`{"on":"31/10/2026","at":"09:30","when":"2026-11-05T10:00"}`), &in); err != nil {
		t.Fatal(err)
	}

	if msg := Civil(in.On); msg.Key != "validation.date" {
		t.Errorf("Civil(wrong format) = %v, want validation.date", msg)
	}
	if msg := RequiredCivil(in.On); msg.Key != "validation.date" {
		t.Errorf("RequiredCivil(wrong format) = %v, want validation.date", msg)
	}
	for _, v := range []interface {
		IsZero() bool
		IsValid() bool
		FormatMessage() i18n.Message
	}{in.At, in.When, civil.NewDate(2026, time.October, 31)} {
		if msg := RequiredCivil(v); !msg.IsZero() {
			t.Errorf("RequiredCivil(%v) = %v, want valid", v, msg)
		}
	}
	if msg := Civil(in.Empty); !msg.IsZero() {
		t.Errorf("Civil(missing) = %v, want valid", msg)
	}
	if msg := RequiredCivil(in.Empty); msg.Key != "validation.required" {
		t.Errorf("RequiredCivil(missing) = %v, want validation.required", msg)
	}
}

func TestTimeZone(t *testing.T) {
	if msg := TimeZone("America/Bogota"); !msg.IsZero() {
		t.Errorf("TimeZone(America/Bogota) = %v", msg)
	}
	for _, name := range []string{"", "Local", "Mars/Base", "GMT-5:00"} {
		if msg := TimeZone(name); msg.Key != "validation.time_zone" {
			t.Errorf("TimeZone(%q) = %v, want validation.time_zone", name, msg)
		}
	}
}
