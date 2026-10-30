package auth

import (
	"strings"
	"testing"
)

func TestCommonPasswords(t *testing.T) {
	if n := len(commonPasswords()); n < 3000 {
		t.Fatalf("the list has %d passwords; ASVS asks for at least 3000", n)
	}
	for p := range commonPasswords() {
		if n := len([]rune(p)); n < minPasswordLen || n > maxPasswordLen || p != strings.ToLower(p) {
			t.Fatalf("%q does not belong in the list: it must be lower case and meet the length rule", p)
		}
	}
}

func TestValidatePasswordRejectsCommon(t *testing.T) {
	for _, p := range []string{"12345678", "password1", "PASSWORD1", "Qwerty123"} {
		if msg := validatePassword(p); msg.Key != "validation.common_password" {
			t.Errorf("%q: got %q, want validation.common_password", p, msg.Key)
		}
	}
	for _, p := range []string{"test-password", "a-long-password", "correct horse battery staple"} {
		if msg := validatePassword(p); !msg.IsZero() {
			t.Errorf("%q should be valid: %q", p, msg.Key)
		}
	}
}
