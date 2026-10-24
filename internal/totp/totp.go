// Package totp implementa los códigos de un solo uso basados en tiempo (TOTP, RFC 6238)
// que generan las apps de autenticación (Google Authenticator, Authy, 1Password…).
//
// Usa los parámetros que todas esas apps esperan: HMAC-SHA1, 6 dígitos y pasos de 30 segundos.
// Solo usa la librería estándar.
package totp

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"strings"
	"time"
)

const (
	// Digits es la cantidad de dígitos de cada código.
	Digits = 6
	// Period es cada cuántos segundos cambia el código.
	Period = 30
	// Skew es cuántos pasos antes y después se aceptan, para tolerar relojes desfasados
	// (con 1, un código vale en una ventana de unos 90 segundos).
	Skew = 1
)

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

// GenerateSecret genera un secreto aleatorio de 160 bits en base32, el formato que
// se carga en la app (escaneando el QR o escribiéndolo a mano).
func GenerateSecret() (string, error) {
	b := make([]byte, 20)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("totp: generar secreto: %w", err)
	}
	return b32.EncodeToString(b), nil
}

// Code devuelve el código vigente en el momento t.
func Code(secret string, t time.Time) (string, error) {
	key, err := decodeSecret(secret)
	if err != nil {
		return "", err
	}
	return codeAt(key, stepAt(t), Digits), nil
}

// Validate comprueba un código en el momento t, con la tolerancia de Skew pasos.
// Si es válido devuelve el paso al que corresponde: guardarlo permite rechazar
// el mismo código si se intenta usar de nuevo.
func Validate(secret, code string, t time.Time) (step int64, ok bool, err error) {
	key, err := decodeSecret(secret)
	if err != nil {
		return 0, false, err
	}

	code = strings.ReplaceAll(strings.TrimSpace(code), " ", "")
	if len(code) != Digits {
		return 0, false, nil
	}

	current := stepAt(t)
	for d := int64(-Skew); d <= Skew; d++ {
		s := current + d
		if subtle.ConstantTimeCompare([]byte(codeAt(key, s, Digits)), []byte(code)) == 1 {
			return s, true, nil
		}
	}
	return 0, false, nil
}

// URI arma la dirección otpauth:// que se convierte en código QR para que la app la escanee.
// issuer es el nombre de la aplicación y account, normalmente, el email del usuario.
func URI(issuer, account, secret string) string {
	// Los espacios se codifican como %20 y no como "+": algunas apps muestran el "+" literal.
	label := url.PathEscape(issuer) + ":" + url.PathEscape(account)
	query := fmt.Sprintf("secret=%s&issuer=%s&algorithm=SHA1&digits=%d&period=%d",
		url.PathEscape(secret), url.PathEscape(issuer), Digits, Period)
	return "otpauth://totp/" + label + "?" + query
}

func stepAt(t time.Time) int64 {
	return t.Unix() / Period
}

func decodeSecret(secret string) ([]byte, error) {
	s := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(secret), " ", ""))
	key, err := b32.DecodeString(strings.TrimRight(s, "="))
	if err != nil || len(key) == 0 {
		return nil, fmt.Errorf("totp: secreto inválido")
	}
	return key, nil
}

// codeAt calcula el código HOTP (RFC 4226) para un paso: HMAC-SHA1 del número de paso,
// truncamiento dinámico y los últimos "digits" dígitos.
func codeAt(key []byte, step int64, digits int) string {
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(step))

	mac := hmac.New(sha1.New, key)
	mac.Write(msg[:])
	sum := mac.Sum(nil)

	offset := sum[len(sum)-1] & 0x0f
	value := binary.BigEndian.Uint32(sum[offset:offset+4]) & 0x7fffffff

	mod := uint32(1)
	for range digits {
		mod *= 10
	}
	return fmt.Sprintf("%0*d", digits, value%mod)
}
