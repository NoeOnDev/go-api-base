package auth

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/argon2"
)

func TestHashAndVerifyPassword(t *testing.T) {
	hash, err := HashPassword(t.Context(), "a strong password")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if !strings.HasPrefix(hash, "$argon2id$v=19$m=19456,t=2,p=1$") {
		t.Errorf("unexpected format: %s", hash)
	}

	ok, err := VerifyPassword(t.Context(), "a strong password", hash)
	if err != nil || !ok {
		t.Errorf("the right password did not verify: ok=%v err=%v", ok, err)
	}

	ok, err = VerifyPassword(t.Context(), "another password", hash)
	if err != nil || ok {
		t.Errorf("a wrong password verified: ok=%v err=%v", ok, err)
	}
}

func TestHashPasswordUsesRandomSalt(t *testing.T) {
	a, _ := HashPassword(t.Context(), "misma")
	b, _ := HashPassword(t.Context(), "misma")
	if a == b {
		t.Error("two hashes of the same password should not be equal")
	}
}

func TestVerifyPasswordInvalidHash(t *testing.T) {
	for _, hash := range []string{"", "texto", "$argon2i$v=19$m=1,t=1,p=1$a$b", "$argon2id$v=18$m=1,t=1,p=1$YQ$Yg"} {
		if _, err := VerifyPassword(t.Context(), "x", hash); err == nil {
			t.Errorf("want an error for %q", hash)
		}
	}
}

func TestPasswordHashingIsBounded(t *testing.T) {
	hash, err := HashPassword(t.Context(), "a strong password")
	if err != nil {
		t.Fatal(err)
	}

	// Every slot is taken, as if one hash per core were running.
	for range cap(hashSlots) {
		hashSlots <- struct{}{}
	}

	// The next one waits its turn, and if the client leaves (the context expires) it computes nothing.
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	if _, err := VerifyPassword(ctx, "a strong password", hash); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("want context.DeadlineExceeded with every slot taken, got %v", err)
	}

	// Once a slot is free, it works again.
	for range cap(hashSlots) {
		<-hashSlots
	}
	if ok, err := VerifyPassword(t.Context(), "a strong password", hash); err != nil || !ok {
		t.Errorf("VerifyPassword = %v, %v", ok, err)
	}
}

// hashWithParams builds a PHC-format hash with other parameters, like the ones that would stay
// stored after raising defaultParams.
func hashWithParams(t *testing.T, password string, p argonParams) string {
	t.Helper()
	salt := make([]byte, p.saltLen)
	key := argon2.IDKey([]byte(password), salt, p.time, p.memory, p.threads, p.keyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version, p.memory, p.time, p.threads,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key))
}

func TestNeedsRehash(t *testing.T) {
	current, err := HashPassword(t.Context(), "a strong password")
	if err != nil {
		t.Fatal(err)
	}
	if needsRehash(current) {
		t.Error("a hash with the current parameters does not need a rehash")
	}

	old := hashWithParams(t, "a strong password", argonParams{memory: 8 * 1024, time: 1, threads: 1, saltLen: 16, keyLen: 32})
	if !needsRehash(old) {
		t.Error("a hash with old parameters needs a rehash")
	}
	if ok, err := VerifyPassword(t.Context(), "a strong password", old); err != nil || !ok {
		t.Errorf("the old hash should still verify: %v, %v", ok, err)
	}

	if needsRehash("texto") {
		t.Error("an invalid hash is not rehashed: VerifyPassword already failed")
	}
}
