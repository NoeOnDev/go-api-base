package auth

import (
	"context"
	"crypto/rand"
	"fmt"
	"math/big"
	"strconv"
	"strings"

	"github.com/NoeOnDev/go-api-base/internal/store"
)

const (
	// recoveryCodeCount is how many recovery codes are generated.
	recoveryCodeCount = 10
	// recoveryCodeAlphabet has no characters that get mixed up when copied by hand (0/o, 1/l/i).
	recoveryCodeAlphabet = "abcdefghjkmnpqrstuvwxyz23456789"
)

// newRecoveryCode generates a 10-character code in the format "xxxxx-xxxxx" (about 50 bits).
func newRecoveryCode() (string, error) {
	max := big.NewInt(int64(len(recoveryCodeAlphabet)))
	var b strings.Builder
	for i := range 10 {
		if i == 5 {
			b.WriteByte('-')
		}
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", fmt.Errorf("generate a recovery code: %w", err)
		}
		b.WriteByte(recoveryCodeAlphabet[n.Int64()])
	}
	return b.String(), nil
}

// normalizeRecoveryCode accepts the code however the user types it: with or without a hyphen,
// with spaces or in uppercase.
func normalizeRecoveryCode(code string) string {
	code = strings.ToLower(code)
	return strings.NewReplacer("-", "", " ", "").Replace(code)
}

// recoveryCodeMACInput is what is protected with HMAC: the normalized code, bound to the user.
// That way two users with the same code store different values, and a hash copied to another
// user is useless to them.
func recoveryCodeMACInput(userID int64, code string) []byte {
	return []byte("recovery_code:" + strconv.FormatInt(userID, 10) + ":" + normalizeRecoveryCode(code))
}

// replaceRecoveryCodes generates new codes, stores their HMAC (replacing the previous ones) with
// the queries q (they can be those of a transaction) and returns them in plain text to show them
// to the user only once.
func (s *Service) replaceRecoveryCodes(ctx context.Context, q *store.Queries, userID int64) ([]string, error) {
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
		hashes[i] = box.MAC(recoveryCodeMACInput(userID, codes[i]))
	}

	if err := q.ReplaceRecoveryCodes(ctx, store.ReplaceRecoveryCodesParams{
		UserID:     userID,
		CodeHashes: hashes,
		KeyID:      box.KeyID(),
	}); err != nil {
		return nil, fmt.Errorf("store the recovery codes: %w", err)
	}
	return codes, nil
}

// useRecoveryCode consumes the code if it is valid for the user. Each code works once.
// It is compared with the HMAC of every key of the key ring, so the codes generated before a
// rotation keep working (as long as their key is in ENCRYPTION_PREVIOUS_KEYS).
func (s *Service) useRecoveryCode(ctx context.Context, userID int64, code string) (bool, error) {
	box, err := s.box()
	if err != nil {
		return false, err
	}

	rows, err := s.q.ConsumeRecoveryCode(ctx, store.ConsumeRecoveryCodeParams{
		UserID:     userID,
		CodeHashes: box.MACs(recoveryCodeMACInput(userID, code)),
	})
	if err != nil {
		return false, fmt.Errorf("use a recovery code: %w", err)
	}
	return rows == 1, nil
}
