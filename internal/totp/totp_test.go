package totp

import (
	"net/url"
	"strings"
	"testing"
	"time"
)

// Test vectors from RFC 6238 (appendix B) for SHA1, with 8 digits.
func TestRFC6238Vectors(t *testing.T) {
	key := []byte("12345678901234567890")
	vectors := map[int64]string{
		59:          "94287082",
		1111111109:  "07081804",
		1111111111:  "14050471",
		1234567890:  "89005924",
		2000000000:  "69279037",
		20000000000: "65353130",
	}
	for unix, want := range vectors {
		if got := codeAt(key, unix/Period, 8); got != want {
			t.Errorf("t=%d: code = %s, want %s", unix, got, want)
		}
	}
}

func TestCodeAndValidate(t *testing.T) {
	// It is the RFC key in base32, so the 6-digit code is the last 6 of the vector.
	secret := b32.EncodeToString([]byte("12345678901234567890"))
	now := time.Unix(1111111111, 0)

	code, err := Code(secret, now)
	if err != nil || code != "050471" {
		t.Fatalf("Code = %q (%v), want 050471", code, err)
	}

	step, ok, err := Validate(secret, code, now)
	if err != nil || !ok || step != 1111111111/Period {
		t.Errorf("Validate = %d, %v, %v", step, ok, err)
	}

	// The previous and the next step are accepted (clocks out of sync), but nothing further.
	for offset, wantOK := range map[time.Duration]bool{
		-30 * time.Second: true,
		30 * time.Second:  true,
		-90 * time.Second: false,
		90 * time.Second:  false,
	} {
		if _, ok, _ := Validate(secret, code, now.Add(offset)); ok != wantOK {
			t.Errorf("skew %v: valid = %v, want %v", offset, ok, wantOK)
		}
	}

	for _, bad := range []string{"", "12345", "1234567", "abcdef", "000000"} {
		if _, ok, _ := Validate(secret, bad, now); ok {
			t.Errorf("the code %q should not be valid", bad)
		}
	}

	// Spaces in the code (as some apps show them) are ignored.
	if _, ok, _ := Validate(secret, "050 471", now); !ok {
		t.Error("the code with spaces should be valid")
	}
}

func TestGenerateSecret(t *testing.T) {
	a, err := GenerateSecret()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := GenerateSecret()
	if a == b || len(a) != 32 {
		t.Errorf("secretos inesperados: %q %q", a, b)
	}
	if _, err := Code(a, time.Now()); err != nil {
		t.Errorf("the generated secret does not work: %v", err)
	}
	if _, err := Code("no-es-base32!", time.Now()); err == nil {
		t.Error("want an error with an invalid secret")
	}
}

func TestURI(t *testing.T) {
	uri := URI("My App", "alice@example.com", "JBSWY3DPEHPK3PXP")
	u, err := url.Parse(uri)
	if err != nil {
		t.Fatal(err)
	}
	if u.Scheme != "otpauth" || u.Host != "totp" || !strings.HasPrefix(u.Path, "/My App:alice@example.com") {
		t.Errorf("URI inesperada: %s", uri)
	}
	if strings.Contains(uri, "+") {
		t.Errorf("spaces should be encoded as %%20: %s", uri)
	}
	q := u.Query()
	if q.Get("secret") != "JBSWY3DPEHPK3PXP" || q.Get("issuer") != "My App" || q.Get("digits") != "6" || q.Get("period") != "30" {
		t.Errorf("unexpected parameters: %v", q)
	}
}
