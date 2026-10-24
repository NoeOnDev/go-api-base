package auth

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/NoeOnDev/go-api-base/internal/secretbox"
	"github.com/NoeOnDev/go-api-base/internal/store"
)

// KeyReport es el resultado de RotateKeys: qué se volvió a cifrar y qué sigue dependiendo
// de claves viejas.
type KeyReport struct {
	CurrentKeyID string
	// Reencrypted son los secretos del 2FA que se volvieron a cifrar con la clave actual.
	Reencrypted int
	// AlreadyCurrent son los secretos que ya estaban con la clave actual.
	AlreadyCurrent int
	// Undecryptable son los secretos que no se pudieron descifrar: su clave no está configurada
	// o el dato está dañado. Esos usuarios no pueden usar su 2FA hasta que se les desactive.
	Undecryptable []UndecryptableSecret
	// OldRecoveryCodes cuenta, por clave vieja, los usuarios con códigos de recuperación calculados
	// con ella. No se pueden recalcular sin el código original: dejan de servir si se quita la clave,
	// así que esos usuarios tienen que regenerarlos.
	OldRecoveryCodes []KeyUsage
	// RemovableKeys son las claves anteriores que ya no usa ningún dato: se pueden quitar de
	// ENCRYPTION_PREVIOUS_KEYS.
	RemovableKeys []string
}

// UndecryptableSecret es un secreto del 2FA que no se pudo descifrar.
type UndecryptableSecret struct {
	UserID int64
	KeyID  string
	Reason string
}

// KeyUsage es la cantidad de usuarios con datos que dependen de una clave.
type KeyUsage struct {
	KeyID string
	Users int64
}

// Done indica si ya no queda nada que dependa de claves viejas.
func (r KeyReport) Done() bool {
	return len(r.Undecryptable) == 0 && len(r.OldRecoveryCodes) == 0
}

// RotateKeys vuelve a cifrar con la clave actual todos los secretos del 2FA que estén con una
// clave anterior, e informa lo que todavía depende de claves viejas. Se puede correr las veces
// que haga falta: lo que ya está con la clave actual no se toca.
func (s *Service) RotateKeys(ctx context.Context) (KeyReport, error) {
	box, err := s.box()
	if err != nil {
		return KeyReport{}, err
	}
	report := KeyReport{CurrentKeyID: box.KeyID()}
	inUse := map[string]bool{}

	users, err := s.q.ListUsersWithTOTPSecret(ctx)
	if err != nil {
		return report, fmt.Errorf("listar secretos del 2FA: %w", err)
	}

	for _, u := range users {
		old := *u.TOTPSecret
		if !box.NeedsRotation(old) {
			report.AlreadyCurrent++
			continue
		}

		secret, err := box.Decrypt(old, totpAAD(u.ID))
		if err != nil {
			reason := "dato dañado o de una clave distinta"
			if errors.Is(err, secretbox.ErrUnknownKey) {
				reason = "su clave no está configurada"
			}
			keyID := secretbox.KeyIDOf(old)
			report.Undecryptable = append(report.Undecryptable, UndecryptableSecret{UserID: u.ID, KeyID: keyID, Reason: reason})
			inUse[keyID] = true
			continue
		}

		encrypted, err := box.Encrypt(secret, totpAAD(u.ID))
		if err != nil {
			return report, err
		}
		rows, err := s.q.UpdateTOTPSecret(ctx, store.UpdateTOTPSecretParams{ID: u.ID, OldSecret: &old, NewSecret: &encrypted})
		if err != nil {
			return report, fmt.Errorf("guardar secreto del usuario %d: %w", u.ID, err)
		}
		if rows == 1 {
			report.Reencrypted++
		}
	}

	codes, err := s.q.CountRecoveryCodeUsersByKey(ctx)
	if err != nil {
		return report, fmt.Errorf("contar códigos de recuperación: %w", err)
	}
	for _, c := range codes {
		if c.KeyID != box.KeyID() {
			report.OldRecoveryCodes = append(report.OldRecoveryCodes, KeyUsage{KeyID: c.KeyID, Users: c.Users})
			inUse[c.KeyID] = true
		}
	}

	for _, id := range box.KeyIDs()[1:] {
		if !inUse[id] {
			report.RemovableKeys = append(report.RemovableKeys, id)
		}
	}
	slices.Sort(report.RemovableKeys)

	return report, nil
}
