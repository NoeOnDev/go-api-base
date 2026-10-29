package auth

import (
	"cmp"
	"context"
	"errors"
	"slices"

	"github.com/NoeOnDev/go-api-base/internal/secretbox"
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
	// QueuedEmails cuenta, por clave vieja, los correos de la cola cifrados con ella que todavía
	// no salieron (ver AddQueuedEmails). Salen solos en segundos o minutos; si se quita la clave
	// antes, se cancelan.
	QueuedEmails []KeyCount
	// RemovableKeys son las claves anteriores que ya no usa ningún dato: se pueden quitar de
	// ENCRYPTION_PREVIOUS_KEYS.
	RemovableKeys []string
}

// KeyCount es la cantidad de datos (por ejemplo, correos en la cola) que dependen de una clave.
type KeyCount struct {
	KeyID string
	Count int64
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
	return len(r.Undecryptable) == 0 && len(r.OldRecoveryCodes) == 0 && len(r.QueuedEmails) == 0
}

// AddQueuedEmails suma al reporte los correos de la cola cifrados con claves anteriores. byKey
// es lo que devuelve jobs.QueuedEmailKeys: auth no conoce la cola, así que RotateKeys no los
// cuenta solo. Mientras queden correos con una clave, esa clave no se puede quitar.
func (r *KeyReport) AddQueuedEmails(byKey map[string]int64) {
	for id, n := range byKey {
		if id == r.CurrentKeyID || n == 0 {
			continue
		}
		r.QueuedEmails = append(r.QueuedEmails, KeyCount{KeyID: id, Count: n})
		r.RemovableKeys = slices.DeleteFunc(r.RemovableKeys, func(k string) bool { return k == id })
	}
	slices.SortFunc(r.QueuedEmails, func(a, b KeyCount) int { return cmp.Compare(a.KeyID, b.KeyID) })
}

// errNoBox indica que falta ENCRYPTION_KEY: sin ella no se puede guardar el secreto del 2FA.
var errNoBox = errors.New("2FA no disponible: falta configurar ENCRYPTION_KEY")

// box devuelve el llavero de ENCRYPTION_KEY, o errNoBox si no se configuró.
func (s *Service) box() (*secretbox.Box, error) {
	if s.opts.Box == nil {
		return nil, errNoBox
	}
	return s.opts.Box, nil
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

	// Lo que cifra o protege el 2FA (ver two_factor.go).
	if err := s.rotateTwoFactorKeys(ctx, box, &report, inUse); err != nil {
		return report, err
	}

	for _, id := range box.KeyIDs()[1:] {
		if !inUse[id] {
			report.RemovableKeys = append(report.RemovableKeys, id)
		}
	}
	slices.Sort(report.RemovableKeys)

	return report, nil
}
