// Package secretbox encrypts sensitive data before storing it in the database (for example, the
// 2FA secret) and computes HMACs to store values that are only compared, like the recovery
// codes.
//
// It works with a keyring: a current key (ENCRYPTION_KEY), which encrypts everything new, and
// previous keys (ENCRYPTION_PREVIOUS_KEYS), which are only used to read what was encrypted
// before a rotation. Each encrypted value carries the identifier of its key ("3f9a01c2:…"), so
// it is known which one decrypts it and whether it has to be encrypted again with the current
// one.
//
// From each 32-byte master key, HKDF derives a key to encrypt (AES-256-GCM), another for the
// HMAC and the identifier. It uses only the standard library.
//
// Encrypt and Decrypt take associated data (aad): a context that is not encrypted but stays
// bound to the value, for example "users.totp_secret:42". Decrypting with another context fails,
// so a value copied to another user's row is useless.
//
// If a key is lost, what was encrypted with it cannot be recovered. See docs/encryption.md.
package secretbox

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// KeySize is the size of each master key in bytes.
const KeySize = 32

var (
	// ErrUnknownKey means the value was encrypted with a key that is not in the keyring
	// (for example, it was removed from ENCRYPTION_PREVIOUS_KEYS before the rotation finished).
	ErrUnknownKey = errors.New("secretbox: the value was encrypted with a key that is not configured")
	// ErrDecrypt means the value is tampered with, does not match its key or was decrypted with
	// another context (aad).
	ErrDecrypt = errors.New("secretbox: could not decrypt (tampered value or different key)")
)

// key holds the keys derived from a master key.
type key struct {
	id     string
	aead   cipher.AEAD
	macKey []byte
}

// Box is the keyring: it encrypts with the current key and decrypts with the matching one.
type Box struct {
	current *key
	all     []*key // the current one first, then the previous ones
	byID    map[string]*key
}

// ParseKey decodes a base64 key (as `make key` generates it).
func ParseKey(encoded string) ([]byte, error) {
	k, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encoded))
	if err != nil || len(k) != KeySize {
		return nil, fmt.Errorf("the key must be %d bytes in base64 (generate it with: make key)", KeySize)
	}
	return k, nil
}

// GenerateKey generates a new master key, in base64.
func GenerateKey() (string, error) {
	k := make([]byte, KeySize)
	if _, err := rand.Read(k); err != nil {
		return "", fmt.Errorf("secretbox: generate key: %w", err)
	}
	return base64.StdEncoding.EncodeToString(k), nil
}

// New creates the keyring with the current key and, optionally, the previous ones.
func New(current []byte, previous ...[]byte) (*Box, error) {
	b := &Box{byID: map[string]*key{}}
	for i, master := range append([][]byte{current}, previous...) {
		k, err := deriveKey(master)
		if err != nil {
			return nil, err
		}
		if _, dup := b.byID[k.id]; dup {
			return nil, fmt.Errorf("secretbox: key %d is repeated (identifier %s)", i, k.id)
		}
		b.byID[k.id] = k
		b.all = append(b.all, k)
	}
	b.current = b.all[0]
	return b, nil
}

func deriveKey(master []byte) (*key, error) {
	if len(master) != KeySize {
		return nil, fmt.Errorf("secretbox: each key must be %d bytes", KeySize)
	}

	encKey, err := hkdf.Key(sha256.New, master, nil, "secretbox encryption v1", 32)
	if err != nil {
		return nil, err
	}
	macKey, err := hkdf.Key(sha256.New, master, nil, "secretbox mac v1", 32)
	if err != nil {
		return nil, err
	}
	id, err := hkdf.Key(sha256.New, master, nil, "secretbox key id v1", 4)
	if err != nil {
		return nil, err
	}

	block, err := aes.NewCipher(encKey)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	return &key{id: hex.EncodeToString(id), aead: aead, macKey: macKey}, nil
}

// KeyID is the identifier of the current key.
func (b *Box) KeyID() string { return b.current.id }

// KeyIDs are the identifiers of every key: the current one first.
func (b *Box) KeyIDs() []string {
	ids := make([]string, len(b.all))
	for i, k := range b.all {
		ids[i] = k.id
	}
	return ids
}

// Encrypt encrypts with the current key and returns "<identifier>:<base64>". aad is the context
// of the value (see the package comment); the same one must be passed to Decrypt.
// Encrypting the same thing twice gives different results (random nonce).
func (b *Box) Encrypt(plaintext, aad []byte) (string, error) {
	k := b.current
	nonce := make([]byte, k.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("secretbox: generate nonce: %w", err)
	}
	sealed := k.aead.Seal(nonce, nonce, plaintext, aad)
	return k.id + ":" + base64.StdEncoding.EncodeToString(sealed), nil
}

// Decrypt decrypts what Encrypt returned, with the key its identifier names and the same
// context (aad). It returns ErrUnknownKey if the key is not in the keyring, and ErrDecrypt if
// the value is tampered with or the context does not match.
func (b *Box) Decrypt(data string, aad []byte) ([]byte, error) {
	id, encoded, ok := strings.Cut(data, ":")
	if !ok {
		return nil, ErrDecrypt
	}
	k, ok := b.byID[id]
	if !ok {
		return nil, ErrUnknownKey
	}

	sealed, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(sealed) < k.aead.NonceSize() {
		return nil, ErrDecrypt
	}
	nonce, ciphertext := sealed[:k.aead.NonceSize()], sealed[k.aead.NonceSize():]
	plaintext, err := k.aead.Open(nil, nonce, ciphertext, aad)
	if err != nil {
		return nil, ErrDecrypt
	}
	return plaintext, nil
}

// KeyIDOf returns the key identifier of an encrypted value, or "" if it has none.
func KeyIDOf(data string) string {
	id, _, ok := strings.Cut(data, ":")
	if !ok {
		return ""
	}
	return id
}

// NeedsRotation reports whether the value is not encrypted with the current key and should be
// encrypted again.
func (b *Box) NeedsRotation(data string) bool {
	return KeyIDOf(data) != b.current.id
}

// MAC computes the HMAC-SHA256 with the current key. It is used to store values that are later
// only compared (like the recovery codes): without the key, whoever reads the database cannot
// try values by brute force.
func (b *Box) MAC(data []byte) []byte {
	return mac(b.current, data)
}

// MACs computes the HMAC with each key of the keyring (the current one first). It is used to
// verify a value stored before a rotation.
func (b *Box) MACs(data []byte) [][]byte {
	out := make([][]byte, len(b.all))
	for i, k := range b.all {
		out[i] = mac(k, data)
	}
	return out
}

func mac(k *key, data []byte) []byte {
	m := hmac.New(sha256.New, k.macKey)
	m.Write(data)
	return m.Sum(nil)
}
