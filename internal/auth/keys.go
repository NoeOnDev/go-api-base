package auth

import (
	"cmp"
	"context"
	"errors"
	"slices"

	"github.com/NoeOnDev/go-api-base/internal/secretbox"
)

// KeyReport is the result of RotateKeys: what was encrypted again and what still depends on
// old keys.
type KeyReport struct {
	CurrentKeyID string
	// Reencrypted are the two-factor secrets that were encrypted again with the current key.
	Reencrypted int
	// AlreadyCurrent are the secrets that already used the current key.
	AlreadyCurrent int
	// Undecryptable are the secrets that could not be decrypted: their key is not configured or the
	// data is damaged. Those users cannot use their two-factor authentication until it is turned off.
	Undecryptable []UndecryptableSecret
	// OldRecoveryCodes counts, per old key, the users with recovery codes computed with it. They
	// cannot be computed again without the original code: they stop working if the key is removed,
	// so those users have to regenerate them.
	OldRecoveryCodes []KeyUsage
	// QueuedEmails counts, per old key, the queued emails encrypted with it that have not been sent
	// yet (see AddQueuedEmails). They go out on their own in seconds or minutes; if the key is
	// removed before, they are cancelled.
	QueuedEmails []KeyCount
	// RemovableKeys are the previous keys that no data uses anymore: they can be removed from
	// ENCRYPTION_PREVIOUS_KEYS.
	RemovableKeys []string
}

// KeyCount is the amount of data (for example, queued emails) that depends on a key.
type KeyCount struct {
	KeyID string
	Count int64
}

// UndecryptableSecret is a two-factor secret that could not be decrypted.
type UndecryptableSecret struct {
	UserID int64
	KeyID  string
	Reason string
}

// KeyUsage is the number of users with data that depends on a key.
type KeyUsage struct {
	KeyID string
	Users int64
}

// Done reports whether nothing depends on old keys anymore.
func (r KeyReport) Done() bool {
	return len(r.Undecryptable) == 0 && len(r.OldRecoveryCodes) == 0 && len(r.QueuedEmails) == 0
}

// AddQueuedEmails adds to the report the queued emails encrypted with previous keys. byKey is
// what jobs.QueuedEmailKeys returns: auth does not know the queue, so RotateKeys does not count
// them on its own. While emails with a key remain, that key cannot be removed.
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

// errNoBox means ENCRYPTION_KEY is missing: without it the two-factor secret cannot be stored.
var errNoBox = errors.New("two-factor authentication unavailable: ENCRYPTION_KEY is not configured")

// box returns the ENCRYPTION_KEY key ring, or errNoBox if it is not configured.
func (s *Service) box() (*secretbox.Box, error) {
	if s.opts.Box == nil {
		return nil, errNoBox
	}
	return s.opts.Box, nil
}

// RotateKeys encrypts again with the current key every two-factor secret that uses a previous
// key, and reports what still depends on old keys. It can be run as often as needed: what
// already uses the current key is left alone.
func (s *Service) RotateKeys(ctx context.Context) (KeyReport, error) {
	box, err := s.box()
	if err != nil {
		return KeyReport{}, err
	}
	report := KeyReport{CurrentKeyID: box.KeyID()}
	inUse := map[string]bool{}

	// What two-factor authentication encrypts or protects (see two_factor.go).
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
