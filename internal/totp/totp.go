// Package totp implements the time-based one-time codes (TOTP, RFC 6238) that authenticator
// apps generate (Google Authenticator, Authy, 1Password…).
//
// It uses the parameters all those apps expect: HMAC-SHA1, 6 digits and 30-second steps.
// It uses only the standard library.
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
	// Digits is the number of digits of each code.
	Digits = 6
	// Period is how many seconds each code lasts.
	Period = 30
	// Skew is how many steps before and after are accepted, to tolerate clocks out of sync
	// (with 1, a code is valid in a window of about 90 seconds).
	Skew = 1
)

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

// GenerateSecret generates a random 160-bit secret in base32, the format that is loaded into
// the app (by scanning the QR code or typing it by hand).
func GenerateSecret() (string, error) {
	b := make([]byte, 20)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("totp: generate secret: %w", err)
	}
	return b32.EncodeToString(b), nil
}

// Code returns the code valid at time t.
func Code(secret string, t time.Time) (string, error) {
	key, err := decodeSecret(secret)
	if err != nil {
		return "", err
	}
	return codeAt(key, stepAt(t), Digits), nil
}

// Validate checks a code at time t, with a tolerance of Skew steps.
// If it is valid it returns the step it belongs to: storing it allows rejecting the same code
// if someone tries to use it again.
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

// URI builds the otpauth:// address that becomes the QR code for the app to scan.
// issuer is the application name and account, usually, the user's email.
func URI(issuer, account, secret string) string {
	// Spaces are encoded as %20 and not as "+": some apps show the "+" literally.
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
		return nil, fmt.Errorf("totp: invalid secret")
	}
	return key, nil
}

// codeAt computes the HOTP code (RFC 4226) for a step: HMAC-SHA1 of the step number, dynamic
// truncation and the last "digits" digits.
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
