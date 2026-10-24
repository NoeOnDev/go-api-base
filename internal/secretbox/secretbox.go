// Package secretbox cifra datos sensibles antes de guardarlos en la base (por ejemplo, el
// secreto del 2FA) y calcula HMAC para guardar valores que solo se comparan, como los
// códigos de recuperación.
//
// Usa una clave maestra de 32 bytes (ENCRYPTION_KEY) de la que deriva, con HKDF, una clave
// para cifrar (AES-256-GCM) y otra independiente para el HMAC. Solo usa la librería estándar.
//
// Si se pierde la clave, los datos cifrados no se pueden recuperar: guardala como cualquier
// otro secreto de producción y no la cambies sin volver a cifrar lo guardado.
package secretbox

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
)

// KeySize es el tamaño de la clave maestra en bytes.
const KeySize = 32

// Box cifra, descifra y calcula HMAC con claves derivadas de la clave maestra.
type Box struct {
	aead   cipher.AEAD
	macKey []byte
}

// ParseKey decodifica una clave en base64 (como la genera `openssl rand -base64 32`).
func ParseKey(encoded string) ([]byte, error) {
	key, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(key) != KeySize {
		return nil, fmt.Errorf("la clave debe ser de %d bytes en base64 (generala con: openssl rand -base64 32)", KeySize)
	}
	return key, nil
}

// New crea un Box a partir de la clave maestra de 32 bytes.
func New(masterKey []byte) (*Box, error) {
	if len(masterKey) != KeySize {
		return nil, fmt.Errorf("secretbox: la clave debe tener %d bytes", KeySize)
	}

	encKey, err := hkdf.Key(sha256.New, masterKey, nil, "secretbox encryption v1", 32)
	if err != nil {
		return nil, err
	}
	macKey, err := hkdf.Key(sha256.New, masterKey, nil, "secretbox mac v1", 32)
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

	return &Box{aead: aead, macKey: macKey}, nil
}

// Encrypt cifra los datos y devuelve el resultado en base64 (nonce aleatorio + texto cifrado).
// Cifrar dos veces lo mismo da resultados distintos.
func (b *Box) Encrypt(plaintext []byte) (string, error) {
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("secretbox: generar nonce: %w", err)
	}
	sealed := b.aead.Seal(nonce, nonce, plaintext, nil)
	return base64.StdEncoding.EncodeToString(sealed), nil
}

var errDecrypt = errors.New("secretbox: no se pudo descifrar (dato alterado o clave distinta)")

// Decrypt descifra lo que devolvió Encrypt. Falla si el dato fue alterado o se cifró con otra clave.
func (b *Box) Decrypt(encoded string) ([]byte, error) {
	sealed, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(sealed) < b.aead.NonceSize() {
		return nil, errDecrypt
	}
	nonce, ciphertext := sealed[:b.aead.NonceSize()], sealed[b.aead.NonceSize():]
	plaintext, err := b.aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, errDecrypt
	}
	return plaintext, nil
}

// MAC calcula el HMAC-SHA256 de los datos. Sirve para guardar valores que después solo se
// comparan (como los códigos de recuperación): sin la clave, quien lea la base no puede
// probar códigos por fuerza bruta.
func (b *Box) MAC(data []byte) []byte {
	mac := hmac.New(sha256.New, b.macKey)
	mac.Write(data)
	return mac.Sum(nil)
}
