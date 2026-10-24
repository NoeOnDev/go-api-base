package auth

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestHashAndVerifyPassword(t *testing.T) {
	hash, err := HashPassword(t.Context(), "una contraseña segura")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if !strings.HasPrefix(hash, "$argon2id$v=19$m=19456,t=2,p=1$") {
		t.Errorf("formato inesperado: %s", hash)
	}

	ok, err := VerifyPassword(t.Context(), "una contraseña segura", hash)
	if err != nil || !ok {
		t.Errorf("la contraseña correcta no verificó: ok=%v err=%v", ok, err)
	}

	ok, err = VerifyPassword(t.Context(), "otra contraseña", hash)
	if err != nil || ok {
		t.Errorf("una contraseña incorrecta verificó: ok=%v err=%v", ok, err)
	}
}

func TestHashPasswordUsesRandomSalt(t *testing.T) {
	a, _ := HashPassword(t.Context(), "misma")
	b, _ := HashPassword(t.Context(), "misma")
	if a == b {
		t.Error("dos hashes de la misma contraseña no deberían ser iguales")
	}
}

func TestVerifyPasswordInvalidHash(t *testing.T) {
	for _, hash := range []string{"", "texto", "$argon2i$v=19$m=1,t=1,p=1$a$b", "$argon2id$v=18$m=1,t=1,p=1$YQ$Yg"} {
		if _, err := VerifyPassword(t.Context(), "x", hash); err == nil {
			t.Errorf("se esperaba error para %q", hash)
		}
	}
}

func TestPasswordHashingIsBounded(t *testing.T) {
	hash, err := HashPassword(t.Context(), "una contraseña segura")
	if err != nil {
		t.Fatal(err)
	}

	// Se ocupan todos los lugares, como si hubiera un hash por núcleo en curso.
	for range cap(hashSlots) {
		hashSlots <- struct{}{}
	}

	// El siguiente espera su turno, y si el cliente se va (el contexto vence) no calcula nada.
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	if _, err := VerifyPassword(ctx, "una contraseña segura", hash); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("se esperaba context.DeadlineExceeded con todos los lugares ocupados, se obtuvo %v", err)
	}

	// Al liberarse un lugar, vuelve a funcionar.
	for range cap(hashSlots) {
		<-hashSlots
	}
	if ok, err := VerifyPassword(t.Context(), "una contraseña segura", hash); err != nil || !ok {
		t.Errorf("VerifyPassword = %v, %v", ok, err)
	}
}
