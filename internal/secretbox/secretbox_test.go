package secretbox

import (
	"bytes"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
)

func masterKey(fill byte) []byte { return bytes.Repeat([]byte{fill}, KeySize) }

// aad is the test context, like the one auth uses for the 2FA secret.
var aad = []byte("users.totp_secret:1")

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

	a, err := b.Encrypt([]byte("JBSWY3DPEHPK3PXP"), aad)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(a, b.KeyID()+":") || len(b.KeyID()) != 8 {
		t.Errorf("the value does not carry the key identifier: %q (id %q)", a, b.KeyID())
	}
	c, _ := b.Encrypt([]byte("JBSWY3DPEHPK3PXP"), aad)
	if a == c {
		t.Error("encrypting the same thing twice should give different results (random nonce)")
	}

	got, err := b.Decrypt(a, aad)
	if err != nil || string(got) != "JBSWY3DPEHPK3PXP" {
		t.Errorf("Decrypt = %q, %v", got, err)
	}
	if b.NeedsRotation(a) {
		t.Error("a value of the current key does not need rotation")
	}
}

func TestRotation(t *testing.T) {
	oldBox := testBox(t, 1)
	old, _ := oldBox.Encrypt([]byte("secret"), aad)

	// After rotating: new key (2) and the old one (1) as previous.
	rotated := testBox(t, 2, 1)
	if rotated.KeyID() == oldBox.KeyID() {
		t.Fatal("two different keys should have different identifiers")
	}

	got, err := rotated.Decrypt(old, aad)
	if err != nil || string(got) != "secret" {
		t.Fatalf("the previous key should still decrypt: %q, %v", got, err)
	}
	if !rotated.NeedsRotation(old) {
		t.Error("a value of the previous key should need rotation")
	}

	fresh, _ := rotated.Encrypt(got, aad)
	if !strings.HasPrefix(fresh, rotated.KeyID()+":") || rotated.NeedsRotation(fresh) {
		t.Errorf("new values should be encrypted with the current key: %q", fresh)
	}

	// If the old key is removed before the rotation finishes, the error says so.
	if _, err := testBox(t, 2).Decrypt(old, aad); !errors.Is(err, ErrUnknownKey) {
		t.Errorf("want ErrUnknownKey, got %v", err)
	}

	if ids := rotated.KeyIDs(); len(ids) != 2 || ids[0] != rotated.KeyID() || ids[1] != oldBox.KeyID() {
		t.Errorf("KeyIDs = %v", ids)
	}
}

func TestAADBindsContext(t *testing.T) {
	b := testBox(t, 1)
	enc, err := b.Encrypt([]byte("alice's secret"), []byte("users.totp_secret:1"))
	if err != nil {
		t.Fatal(err)
	}

	// The same value, copied to another user's row, cannot be decrypted.
	for name, other := range map[string][]byte{
		"another user":  []byte("users.totp_secret:2"),
		"another field": []byte("users.api_token:1"),
		"no context":    nil,
	} {
		if _, err := b.Decrypt(enc, other); !errors.Is(err, ErrDecrypt) {
			t.Errorf("%s: want ErrDecrypt, got %v", name, err)
		}
	}
}

func TestDecryptRejectsTampering(t *testing.T) {
	b := testBox(t, 1)
	enc, _ := b.Encrypt([]byte("secret"), aad)

	id, encoded, _ := strings.Cut(enc, ":")
	raw, _ := base64.StdEncoding.DecodeString(encoded)
	raw[len(raw)-1] ^= 1
	tampered := id + ":" + base64.StdEncoding.EncodeToString(raw)

	for name, value := range map[string]string{
		"alterado":      tampered,
		"no es base64":  id + ":%%%",
		"too short":     id + ":AAAA",
		"no identifier": "AAAA",
	} {
		if _, err := b.Decrypt(value, aad); !errors.Is(err, ErrDecrypt) {
			t.Errorf("%s: want ErrDecrypt, got %v", name, err)
		}
	}
}

func TestMACs(t *testing.T) {
	b := testBox(t, 2, 1)
	if !bytes.Equal(b.MAC([]byte("abc")), b.MAC([]byte("abc"))) {
		t.Error("the MAC should be deterministic")
	}

	macs := b.MACs([]byte("abc"))
	if len(macs) != 2 || !bytes.Equal(macs[0], b.MAC([]byte("abc"))) {
		t.Fatalf("MACs should start with the current key's: %d", len(macs))
	}
	// The second one is what the previous key would have computed.
	if !bytes.Equal(macs[1], testBox(t, 1).MAC([]byte("abc"))) {
		t.Error("the previous key's MAC does not match")
	}
}

func TestNewAndKeys(t *testing.T) {
	if _, err := New(masterKey(1), masterKey(1)); err == nil {
		t.Error("New should reject a repeated key")
	}
	if _, err := New([]byte("short")); err == nil {
		t.Error("New should reject a short key")
	}

	generated, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseKey(generated); err != nil {
		t.Errorf("the generated key is not valid: %v", err)
	}
	for _, bad := range []string{"", "short", base64.StdEncoding.EncodeToString([]byte("only 16 bytes!!!"))} {
		if _, err := ParseKey(bad); err == nil {
			t.Errorf("key %q should be invalid", bad)
		}
	}
}
