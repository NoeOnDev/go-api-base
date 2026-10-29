package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"sync"

	"golang.org/x/crypto/argon2"
)

// argon2id parameters recommended by OWASP for a server with few resources: 19 MiB of memory,
// 2 iterations and 1 thread. Hashing a password takes a few dozen milliseconds: unnoticeable for
// the user, but very expensive for a brute force attack.
type argonParams struct {
	memory  uint32 // en KiB
	time    uint32
	threads uint8
	saltLen uint32
	keyLen  uint32
}

var defaultParams = argonParams{memory: 19 * 1024, time: 2, threads: 1, saltLen: 16, keyLen: 32}

var errInvalidHash = errors.New("invalid password hash format")

// hashSlots limits how many argon2 hashes are computed at once: one per core. Each hash takes
// 19 MiB, so without a cap 200 simultaneous logins take the process to several GiB and, on a
// small VPS, the system kills it for lack of memory. With the cap, the extra logins wait their
// turn: the total is the same, because the CPU could not do more at once anyway.
var hashSlots = make(chan struct{}, max(1, runtime.GOMAXPROCS(0)))

// argon2Key computes the argon2id key when a slot is free. If the context is cancelled while it
// waits (for example, the client closed the connection), it computes nothing.
func argon2Key(ctx context.Context, password, salt []byte, p argonParams, keyLen uint32) ([]byte, error) {
	select {
	case hashSlots <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-hashSlots }()
	return argon2.IDKey(password, salt, p.time, p.memory, p.threads, keyLen), nil
}

// HashPassword returns the hash of the password in PHC format:
//
//	$argon2id$v=19$m=19456,t=2,p=1$<salt>$<hash>
//
// The parameters travel inside the hash, so they can be raised in the future without breaking
// the stored passwords: each one is hashed again with the new ones the next time its user signs
// in (see needsRehash).
func HashPassword(ctx context.Context, password string) (string, error) {
	p := defaultParams
	salt := make([]byte, p.saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate the salt: %w", err)
	}

	key, err := argon2Key(ctx, []byte(password), salt, p, p.keyLen)
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, p.memory, p.time, p.threads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	), nil
}

// VerifyPassword compares a password with its hash in constant time.
func VerifyPassword(ctx context.Context, password, encoded string) (bool, error) {
	p, salt, key, err := decodeHash(encoded)
	if err != nil {
		return false, err
	}

	other, err := argon2Key(ctx, []byte(password), salt, p, uint32(len(key)))
	if err != nil {
		return false, err
	}
	return subtle.ConstantTimeCompare(key, other) == 1, nil
}

// needsRehash reports whether the hash was computed with parameters other than the current ones
// (for example, before raising them) and should be computed again. An invalid hash returns
// false: with it, VerifyPassword already failed.
func needsRehash(encoded string) bool {
	p, salt, key, err := decodeHash(encoded)
	if err != nil {
		return false
	}
	d := defaultParams
	return p.memory != d.memory || p.time != d.time || p.threads != d.threads ||
		uint32(len(salt)) != d.saltLen || uint32(len(key)) != d.keyLen
}

func decodeHash(encoded string) (argonParams, []byte, []byte, error) {
	var p argonParams

	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return p, nil, nil, errInvalidHash
	}

	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return p, nil, nil, errInvalidHash
	}

	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.memory, &p.time, &p.threads); err != nil {
		return p, nil, nil, errInvalidHash
	}

	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return p, nil, nil, errInvalidHash
	}
	key, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return p, nil, nil, errInvalidHash
	}

	return p, salt, key, nil
}

// dummyHash is the hash compared against when the email does not exist in the login. That way
// the login takes as long whether the user exists or not, and nobody can find out which emails
// are registered by timing the response.
var dummyHash = sync.OnceValue(func() string {
	h, err := HashPassword(context.Background(), "dummy-password-to-even-out-timing")
	if err != nil {
		panic(err) // it only fails if the system cannot generate random numbers
	}
	return h
})
