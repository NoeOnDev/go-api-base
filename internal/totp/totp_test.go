package totp

import (
	"net/url"
	"strings"
	"testing"
	"time"
)

// Vectores de prueba del RFC 6238 (apéndice B) para SHA1, con 8 dígitos.
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
			t.Errorf("t=%d: código = %s, se esperaba %s", unix, got, want)
		}
	}
}

func TestCodeAndValidate(t *testing.T) {
	// Es la clave del RFC en base32, así que el código de 6 dígitos son los últimos 6 del vector.
	secret := b32.EncodeToString([]byte("12345678901234567890"))
	now := time.Unix(1111111111, 0)

	code, err := Code(secret, now)
	if err != nil || code != "050471" {
		t.Fatalf("Code = %q (%v), se esperaba 050471", code, err)
	}

	step, ok, err := Validate(secret, code, now)
	if err != nil || !ok || step != 1111111111/Period {
		t.Errorf("Validate = %d, %v, %v", step, ok, err)
	}

	// Se aceptan el paso anterior y el siguiente (relojes desfasados), pero no más lejos.
	for offset, wantOK := range map[time.Duration]bool{
		-30 * time.Second: true,
		30 * time.Second:  true,
		-90 * time.Second: false,
		90 * time.Second:  false,
	} {
		if _, ok, _ := Validate(secret, code, now.Add(offset)); ok != wantOK {
			t.Errorf("desfase %v: válido = %v, se esperaba %v", offset, ok, wantOK)
		}
	}

	for _, bad := range []string{"", "12345", "1234567", "abcdef", "000000"} {
		if _, ok, _ := Validate(secret, bad, now); ok {
			t.Errorf("el código %q no debería ser válido", bad)
		}
	}

	// Espacios en el código (como los muestran algunas apps) se ignoran.
	if _, ok, _ := Validate(secret, "050 471", now); !ok {
		t.Error("el código con espacios debería ser válido")
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
		t.Errorf("el secreto generado no sirve: %v", err)
	}
	if _, err := Code("no-es-base32!", time.Now()); err == nil {
		t.Error("se esperaba error con un secreto inválido")
	}
}

func TestURI(t *testing.T) {
	uri := URI("Mi App", "ana@ejemplo.com", "JBSWY3DPEHPK3PXP")
	u, err := url.Parse(uri)
	if err != nil {
		t.Fatal(err)
	}
	if u.Scheme != "otpauth" || u.Host != "totp" || !strings.HasPrefix(u.Path, "/Mi App:ana@ejemplo.com") {
		t.Errorf("URI inesperada: %s", uri)
	}
	q := u.Query()
	if q.Get("secret") != "JBSWY3DPEHPK3PXP" || q.Get("issuer") != "Mi App" || q.Get("digits") != "6" || q.Get("period") != "30" {
		t.Errorf("parámetros inesperados: %v", q)
	}
}
