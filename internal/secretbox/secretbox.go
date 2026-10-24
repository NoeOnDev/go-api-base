// Package secretbox cifra datos sensibles antes de guardarlos en la base (por ejemplo, el
// secreto del 2FA) y calcula HMAC para guardar valores que solo se comparan, como los
// códigos de recuperación.
//
// Trabaja con un llavero: una clave actual (ENCRYPTION_KEY), con la que se cifra todo lo nuevo,
// y claves anteriores (ENCRYPTION_PREVIOUS_KEYS), que solo se usan para leer lo que se cifró antes
// de una rotación. Cada dato cifrado lleva el identificador de su clave ("3f9a01c2:…"), así se sabe
// con cuál descifrarlo y si hace falta volver a cifrarlo con la actual.
//
// De cada clave maestra de 32 bytes se derivan con HKDF una clave para cifrar (AES-256-GCM),
// otra para el HMAC y el identificador. Solo usa la librería estándar.
//
// Si se pierde una clave, lo cifrado con ella no se puede recuperar. Ver docs/encryption.md.
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

// KeySize es el tamaño de cada clave maestra en bytes.
const KeySize = 32

var (
	// ErrUnknownKey indica que el dato se cifró con una clave que no está en el llavero
	// (por ejemplo, se quitó de ENCRYPTION_PREVIOUS_KEYS antes de terminar la rotación).
	ErrUnknownKey = errors.New("secretbox: el dato se cifró con una clave que no está configurada")
	// ErrDecrypt indica que el dato está alterado o no corresponde a ninguna clave.
	ErrDecrypt = errors.New("secretbox: no se pudo descifrar (dato alterado o clave distinta)")
)

// key son las claves derivadas de una clave maestra.
type key struct {
	id     string
	aead   cipher.AEAD
	macKey []byte
}

// Box es el llavero: cifra con la clave actual y descifra con la que corresponda.
type Box struct {
	current *key
	all     []*key // la actual primero, después las anteriores
	byID    map[string]*key
}

// ParseKey decodifica una clave en base64 (como la genera `make key`).
func ParseKey(encoded string) ([]byte, error) {
	k, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encoded))
	if err != nil || len(k) != KeySize {
		return nil, fmt.Errorf("la clave debe ser de %d bytes en base64 (generala con: make key)", KeySize)
	}
	return k, nil
}

// GenerateKey genera una clave maestra nueva, en base64.
func GenerateKey() (string, error) {
	k := make([]byte, KeySize)
	if _, err := rand.Read(k); err != nil {
		return "", fmt.Errorf("secretbox: generar clave: %w", err)
	}
	return base64.StdEncoding.EncodeToString(k), nil
}

// New crea el llavero con la clave actual y, opcionalmente, las anteriores.
func New(current []byte, previous ...[]byte) (*Box, error) {
	b := &Box{byID: map[string]*key{}}
	for i, master := range append([][]byte{current}, previous...) {
		k, err := deriveKey(master)
		if err != nil {
			return nil, err
		}
		if _, dup := b.byID[k.id]; dup {
			return nil, fmt.Errorf("secretbox: la clave %d está repetida (identificador %s)", i, k.id)
		}
		b.byID[k.id] = k
		b.all = append(b.all, k)
	}
	b.current = b.all[0]
	return b, nil
}

func deriveKey(master []byte) (*key, error) {
	if len(master) != KeySize {
		return nil, fmt.Errorf("secretbox: cada clave debe tener %d bytes", KeySize)
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

// KeyID es el identificador de la clave actual.
func (b *Box) KeyID() string { return b.current.id }

// KeyIDs son los identificadores de todas las claves: la actual primero.
func (b *Box) KeyIDs() []string {
	ids := make([]string, len(b.all))
	for i, k := range b.all {
		ids[i] = k.id
	}
	return ids
}

// Encrypt cifra con la clave actual y devuelve "<identificador>:<base64>".
// Cifrar dos veces lo mismo da resultados distintos (nonce aleatorio).
func (b *Box) Encrypt(plaintext []byte) (string, error) {
	k := b.current
	nonce := make([]byte, k.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("secretbox: generar nonce: %w", err)
	}
	sealed := k.aead.Seal(nonce, nonce, plaintext, nil)
	return k.id + ":" + base64.StdEncoding.EncodeToString(sealed), nil
}

// Decrypt descifra lo que devolvió Encrypt, con la clave que indica su identificador.
// Los datos sin identificador (formato anterior a v0.3.1) se prueban con todas las claves.
func (b *Box) Decrypt(data string) ([]byte, error) {
	id, encoded, hasID := strings.Cut(data, ":")
	if !hasID {
		for _, k := range b.all {
			if plaintext, err := open(k, data); err == nil {
				return plaintext, nil
			}
		}
		return nil, ErrDecrypt
	}

	k, ok := b.byID[id]
	if !ok {
		return nil, ErrUnknownKey
	}
	return open(k, encoded)
}

func open(k *key, encoded string) ([]byte, error) {
	sealed, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(sealed) < k.aead.NonceSize() {
		return nil, ErrDecrypt
	}
	nonce, ciphertext := sealed[:k.aead.NonceSize()], sealed[k.aead.NonceSize():]
	plaintext, err := k.aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, ErrDecrypt
	}
	return plaintext, nil
}

// KeyIDOf devuelve el identificador de clave de un dato cifrado, o "" si no tiene (formato anterior).
func KeyIDOf(data string) string {
	id, _, ok := strings.Cut(data, ":")
	if !ok {
		return ""
	}
	return id
}

// NeedsRotation indica si el dato no está cifrado con la clave actual y conviene volver a cifrarlo.
func (b *Box) NeedsRotation(data string) bool {
	return KeyIDOf(data) != b.current.id
}

// MAC calcula el HMAC-SHA256 con la clave actual. Sirve para guardar valores que después solo se
// comparan (como los códigos de recuperación): sin la clave, quien lea la base no puede probar
// valores por fuerza bruta.
func (b *Box) MAC(data []byte) []byte {
	return mac(b.current, data)
}

// MACs calcula el HMAC con cada clave del llavero (la actual primero). Sirve para verificar
// un valor guardado antes de una rotación.
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
