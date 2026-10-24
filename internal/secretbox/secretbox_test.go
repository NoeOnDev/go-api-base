package secretbox

import (
	"bytes"
	"encoding/base64"
	"testing"
)

func testBox(t *testing.T, fill byte) *Box {
	t.Helper()
	b, err := New(bytes.Repeat([]byte{fill}, KeySize))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestEncryptDecrypt(t *testing.T) {
	b := testBox(t, 1)

	a, err := b.Encrypt([]byte("JBSWY3DPEHPK3PXP"))
	if err != nil {
		t.Fatal(err)
	}
	c, _ := b.Encrypt([]byte("JBSWY3DPEHPK3PXP"))
	if a == c {
		t.Error("cifrar dos veces lo mismo debería dar resultados distintos (nonce aleatorio)")
	}

	got, err := b.Decrypt(a)
	if err != nil || string(got) != "JBSWY3DPEHPK3PXP" {
		t.Errorf("Decrypt = %q, %v", got, err)
	}
}

func TestDecryptRejectsTamperingAndOtherKeys(t *testing.T) {
	b := testBox(t, 1)
	enc, _ := b.Encrypt([]byte("secreto"))

	raw, _ := base64.StdEncoding.DecodeString(enc)
	raw[len(raw)-1] ^= 1
	tampered := base64.StdEncoding.EncodeToString(raw)

	for name, value := range map[string]string{
		"alterado":        tampered,
		"no es base64":    "%%%",
		"demasiado corto": "AAAA",
	} {
		if _, err := b.Decrypt(value); err == nil {
			t.Errorf("%s: se esperaba error", name)
		}
	}

	if _, err := testBox(t, 2).Decrypt(enc); err == nil {
		t.Error("otra clave no debería poder descifrar")
	}
}

func TestMAC(t *testing.T) {
	b := testBox(t, 1)
	if !bytes.Equal(b.MAC([]byte("abc")), b.MAC([]byte("abc"))) {
		t.Error("el MAC debería ser determinista")
	}
	if bytes.Equal(b.MAC([]byte("abc")), testBox(t, 2).MAC([]byte("abc"))) {
		t.Error("el MAC debería depender de la clave")
	}
}

func TestParseKey(t *testing.T) {
	valid := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))
	if _, err := ParseKey(valid); err != nil {
		t.Errorf("clave válida rechazada: %v", err)
	}
	for _, bad := range []string{"", "corta", base64.StdEncoding.EncodeToString([]byte("16 bytes nomas!!"))} {
		if _, err := ParseKey(bad); err == nil {
			t.Errorf("clave %q debería ser inválida", bad)
		}
	}
	if _, err := New([]byte("corta")); err == nil {
		t.Error("New debería rechazar una clave corta")
	}
}
