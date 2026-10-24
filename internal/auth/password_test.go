package auth

import (
	"strings"
	"testing"
)

func TestHashAndVerifyPassword(t *testing.T) {
	hash, err := HashPassword("una contraseña segura")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if !strings.HasPrefix(hash, "$argon2id$v=19$m=19456,t=2,p=1$") {
		t.Errorf("formato inesperado: %s", hash)
	}

	ok, err := VerifyPassword("una contraseña segura", hash)
	if err != nil || !ok {
		t.Errorf("la contraseña correcta no verificó: ok=%v err=%v", ok, err)
	}

	ok, err = VerifyPassword("otra contraseña", hash)
	if err != nil || ok {
		t.Errorf("una contraseña incorrecta verificó: ok=%v err=%v", ok, err)
	}
}

func TestHashPasswordUsesRandomSalt(t *testing.T) {
	a, _ := HashPassword("misma")
	b, _ := HashPassword("misma")
	if a == b {
		t.Error("dos hashes de la misma contraseña no deberían ser iguales")
	}
}

func TestVerifyPasswordInvalidHash(t *testing.T) {
	for _, hash := range []string{"", "texto", "$argon2i$v=19$m=1,t=1,p=1$a$b", "$argon2id$v=18$m=1,t=1,p=1$YQ$Yg"} {
		if _, err := VerifyPassword("x", hash); err == nil {
			t.Errorf("se esperaba error para %q", hash)
		}
	}
}
