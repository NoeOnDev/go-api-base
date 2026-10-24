package auth

import (
	"context"
	"crypto/rand"
	"fmt"
	"math/big"
	"strings"

	"github.com/NoeOnDev/go-api-base/internal/store"
)

const (
	// recoveryCodeCount es cuántos códigos de recuperación se generan.
	recoveryCodeCount = 10
	// recoveryCodeAlphabet no tiene caracteres que se confunden al copiarlos a mano (0/o, 1/l/i).
	recoveryCodeAlphabet = "abcdefghjkmnpqrstuvwxyz23456789"
)

// newRecoveryCode genera un código de 10 caracteres con el formato "xxxxx-xxxxx" (unos 50 bits).
func newRecoveryCode() (string, error) {
	max := big.NewInt(int64(len(recoveryCodeAlphabet)))
	var b strings.Builder
	for i := range 10 {
		if i == 5 {
			b.WriteByte('-')
		}
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", fmt.Errorf("generar código de recuperación: %w", err)
		}
		b.WriteByte(recoveryCodeAlphabet[n.Int64()])
	}
	return b.String(), nil
}

// normalizeRecoveryCode acepta el código como lo escriba el usuario: con o sin guion,
// con espacios o en mayúsculas.
func normalizeRecoveryCode(code string) string {
	code = strings.ToLower(code)
	return strings.NewReplacer("-", "", " ", "").Replace(code)
}

// replaceRecoveryCodes genera códigos nuevos, guarda su HMAC (reemplazando los anteriores)
// y los devuelve en texto para mostrárselos al usuario una sola vez.
func (s *Service) replaceRecoveryCodes(ctx context.Context, userID int64) ([]string, error) {
	box, err := s.box()
	if err != nil {
		return nil, err
	}

	codes := make([]string, recoveryCodeCount)
	hashes := make([][]byte, recoveryCodeCount)
	for i := range codes {
		if codes[i], err = newRecoveryCode(); err != nil {
			return nil, err
		}
		hashes[i] = box.MAC([]byte(normalizeRecoveryCode(codes[i])))
	}

	if err := s.q.ReplaceRecoveryCodes(ctx, store.ReplaceRecoveryCodesParams{
		UserID:     userID,
		CodeHashes: hashes,
		KeyID:      box.KeyID(),
	}); err != nil {
		return nil, fmt.Errorf("guardar códigos de recuperación: %w", err)
	}
	return codes, nil
}

// useRecoveryCode consume el código si es válido para el usuario. Cada código sirve una vez.
// Se compara con el HMAC de todas las claves del llavero, así siguen sirviendo los códigos
// generados antes de una rotación (mientras su clave esté en ENCRYPTION_PREVIOUS_KEYS).
func (s *Service) useRecoveryCode(ctx context.Context, userID int64, code string) (bool, error) {
	box, err := s.box()
	if err != nil {
		return false, err
	}

	rows, err := s.q.ConsumeRecoveryCode(ctx, store.ConsumeRecoveryCodeParams{
		UserID:     userID,
		CodeHashes: box.MACs([]byte(normalizeRecoveryCode(code))),
	})
	if err != nil {
		return false, fmt.Errorf("usar código de recuperación: %w", err)
	}
	return rows == 1, nil
}
