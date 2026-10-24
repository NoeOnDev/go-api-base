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

// Parámetros de argon2id recomendados por OWASP para un servidor con pocos recursos:
// 19 MiB de memoria, 2 iteraciones y 1 hilo. Hashear una contraseña tarda unas decenas
// de milisegundos: imperceptible para el usuario, pero muy caro para un ataque de fuerza bruta.
type argonParams struct {
	memory  uint32 // en KiB
	time    uint32
	threads uint8
	saltLen uint32
	keyLen  uint32
}

var defaultParams = argonParams{memory: 19 * 1024, time: 2, threads: 1, saltLen: 16, keyLen: 32}

var errInvalidHash = errors.New("hash de contraseña con formato inválido")

// hashSlots limita cuántos hashes de argon2 se calculan a la vez: uno por núcleo. Cada hash
// reserva 19 MiB, así que sin tope 200 logins simultáneos llevan el proceso a varios GiB y,
// en un VPS chico, el sistema lo mata por falta de memoria. Con el tope, los logins de más
// esperan su turno: el total es el mismo, porque la CPU ya no daba para más a la vez.
var hashSlots = make(chan struct{}, max(1, runtime.GOMAXPROCS(0)))

// argon2Key calcula la clave de argon2id cuando hay un lugar libre. Si el contexto se
// cancela mientras espera (por ejemplo, el cliente cortó la conexión), no calcula nada.
func argon2Key(ctx context.Context, password, salt []byte, p argonParams, keyLen uint32) ([]byte, error) {
	select {
	case hashSlots <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-hashSlots }()
	return argon2.IDKey(password, salt, p.time, p.memory, p.threads, keyLen), nil
}

// HashPassword devuelve el hash de la contraseña en formato PHC:
//
//	$argon2id$v=19$m=19456,t=2,p=1$<sal>$<hash>
//
// Los parámetros viajan dentro del hash, así que se pueden subir en el futuro
// sin romper las contraseñas ya guardadas.
func HashPassword(ctx context.Context, password string) (string, error) {
	p := defaultParams
	salt := make([]byte, p.saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generar sal: %w", err)
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

// VerifyPassword compara una contraseña con su hash en tiempo constante.
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

// dummyHash es el hash contra el que se compara cuando el email no existe en el login.
// Así el login tarda lo mismo exista o no el usuario, y no se puede averiguar qué emails
// están registrados midiendo el tiempo de respuesta.
var dummyHash = sync.OnceValue(func() string {
	h, err := HashPassword(context.Background(), "contraseña-ficticia-para-igualar-tiempos")
	if err != nil {
		panic(err) // solo falla si el sistema no puede generar números aleatorios
	}
	return h
})
