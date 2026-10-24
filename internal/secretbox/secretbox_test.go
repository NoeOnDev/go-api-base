package secretbox

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
)

func masterKey(fill byte) []byte { return bytes.Repeat([]byte{fill}, KeySize) }

func testBox(t *testing.T, current byte, previous ...byte) *Box {
	t.Helper()
	prev := make([][]byte, len(previous))
	for i, p := range previous {
		prev[i] = masterKey(p)
	}
	b, err := New(masterKey(current), prev...)
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
	if !strings.HasPrefix(a, b.KeyID()+":") || len(b.KeyID()) != 8 {
		t.Errorf("el dato no lleva el identificador de la clave: %q (id %q)", a, b.KeyID())
	}
	c, _ := b.Encrypt([]byte("JBSWY3DPEHPK3PXP"))
	if a == c {
		t.Error("cifrar dos veces lo mismo debería dar resultados distintos (nonce aleatorio)")
	}

	got, err := b.Decrypt(a)
	if err != nil || string(got) != "JBSWY3DPEHPK3PXP" {
		t.Errorf("Decrypt = %q, %v", got, err)
	}
	if b.NeedsRotation(a) {
		t.Error("un dato de la clave actual no necesita rotación")
	}
}

func TestRotation(t *testing.T) {
	oldBox := testBox(t, 1)
	old, _ := oldBox.Encrypt([]byte("secreto"))

	// Después de rotar: clave nueva (2) y la vieja (1) como anterior.
	rotated := testBox(t, 2, 1)
	if rotated.KeyID() == oldBox.KeyID() {
		t.Fatal("dos claves distintas deberían tener identificadores distintos")
	}

	got, err := rotated.Decrypt(old)
	if err != nil || string(got) != "secreto" {
		t.Fatalf("la clave anterior debería seguir descifrando: %q, %v", got, err)
	}
	if !rotated.NeedsRotation(old) {
		t.Error("un dato de la clave anterior debería necesitar rotación")
	}

	fresh, _ := rotated.Encrypt(got)
	if !strings.HasPrefix(fresh, rotated.KeyID()+":") || rotated.NeedsRotation(fresh) {
		t.Errorf("lo nuevo debería cifrarse con la clave actual: %q", fresh)
	}

	// Si se quita la clave vieja antes de terminar la rotación, el error lo dice.
	if _, err := testBox(t, 2).Decrypt(old); !errors.Is(err, ErrUnknownKey) {
		t.Errorf("se esperaba ErrUnknownKey, se obtuvo %v", err)
	}

	if ids := rotated.KeyIDs(); len(ids) != 2 || ids[0] != rotated.KeyID() || ids[1] != oldBox.KeyID() {
		t.Errorf("KeyIDs = %v", ids)
	}
}

func TestDecryptLegacyFormat(t *testing.T) {
	// Formato de v0.3.0: base64(nonce + texto cifrado), sin identificador.
	k, err := deriveKey(masterKey(1))
	if err != nil {
		t.Fatal(err)
	}
	nonce := make([]byte, k.aead.NonceSize())
	_, _ = rand.Read(nonce)
	legacy := base64.StdEncoding.EncodeToString(k.aead.Seal(nonce, nonce, []byte("viejo"), nil))

	b := testBox(t, 2, 1)
	got, err := b.Decrypt(legacy)
	if err != nil || string(got) != "viejo" {
		t.Errorf("el formato anterior debería descifrarse con alguna clave: %q, %v", got, err)
	}
	if !b.NeedsRotation(legacy) {
		t.Error("el formato anterior debería necesitar rotación")
	}
}

func TestDecryptRejectsTampering(t *testing.T) {
	b := testBox(t, 1)
	enc, _ := b.Encrypt([]byte("secreto"))

	id, encoded, _ := strings.Cut(enc, ":")
	raw, _ := base64.StdEncoding.DecodeString(encoded)
	raw[len(raw)-1] ^= 1
	tampered := id + ":" + base64.StdEncoding.EncodeToString(raw)

	for name, value := range map[string]string{
		"alterado":        tampered,
		"no es base64":    id + ":%%%",
		"demasiado corto": id + ":AAAA",
		"sin prefijo":     "AAAA",
	} {
		if _, err := b.Decrypt(value); !errors.Is(err, ErrDecrypt) {
			t.Errorf("%s: se esperaba ErrDecrypt, se obtuvo %v", name, err)
		}
	}
}

func TestMACs(t *testing.T) {
	b := testBox(t, 2, 1)
	if !bytes.Equal(b.MAC([]byte("abc")), b.MAC([]byte("abc"))) {
		t.Error("el MAC debería ser determinista")
	}

	macs := b.MACs([]byte("abc"))
	if len(macs) != 2 || !bytes.Equal(macs[0], b.MAC([]byte("abc"))) {
		t.Fatalf("MACs debería empezar por el de la clave actual: %d", len(macs))
	}
	// El segundo es el que habría calculado la clave anterior.
	if !bytes.Equal(macs[1], testBox(t, 1).MAC([]byte("abc"))) {
		t.Error("el MAC de la clave anterior no coincide")
	}
}

func TestNewAndKeys(t *testing.T) {
	if _, err := New(masterKey(1), masterKey(1)); err == nil {
		t.Error("New debería rechazar una clave repetida")
	}
	if _, err := New([]byte("corta")); err == nil {
		t.Error("New debería rechazar una clave corta")
	}

	generated, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseKey(generated); err != nil {
		t.Errorf("la clave generada no es válida: %v", err)
	}
	for _, bad := range []string{"", "corta", base64.StdEncoding.EncodeToString([]byte("16 bytes nomas!!"))} {
		if _, err := ParseKey(bad); err == nil {
			t.Errorf("clave %q debería ser inválida", bad)
		}
	}
}
