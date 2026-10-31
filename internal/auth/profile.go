package auth

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/NoeOnDev/go-api-base/internal/httpx"
	"github.com/NoeOnDev/go-api-base/internal/i18n"
	"github.com/NoeOnDev/go-api-base/internal/store"
	"github.com/NoeOnDev/go-api-base/internal/validate"
)

type profileInput struct {
	Name string `json:"name"`
	// Locale and TimeZone are optional: if one is missing, it does not change.
	Locale   *string `json:"locale"`
	TimeZone *string `json:"time_zone"`
}

// updateProfile answers PUT /auth/me: it changes the user's name and, if given, their language
// (one of the available ones; see docs/i18n.md) and their time zone (see docs/time.md). The email is not changed here: changing it
// requires verifying the new address.
func (s *Service) updateProfile(w http.ResponseWriter, r *http.Request) error {
	var in profileInput
	if err := httpx.Decode(w, r, &in); err != nil {
		return err
	}
	in.Name = strings.TrimSpace(in.Name)
	fields := httpx.Fields{}
	if msg := validate.Required(in.Name, maxNameLen); !msg.IsZero() {
		fields["name"] = msg
	}
	if in.Locale != nil {
		locale, msg := s.lookupLocale(*in.Locale)
		if !msg.IsZero() {
			fields["locale"] = msg
		}
		in.Locale = &locale
	}
	if msg := validateTimeZone(in.TimeZone); !msg.IsZero() {
		fields["time_zone"] = msg
	}
	if len(fields) > 0 {
		return httpx.Validation(fields)
	}

	u, err := s.q.UpdateUserProfile(r.Context(), store.UpdateUserProfileParams{
		ID:       MustUser(r.Context()).ID,
		Name:     in.Name,
		Locale:   in.Locale,
		TimeZone: in.TimeZone,
	})
	if err != nil {
		return fmt.Errorf("update the profile: %w", err)
	}

	httpx.Write(w, http.StatusOK, toUser(u))
	return nil
}

type changePasswordInput struct {
	CurrentPassword string `json:"current_password"`
	Password        string `json:"password"`
}

// changePassword answers PUT /auth/password. It asks for the current password (so someone who
// finds the session open cannot change it) and closes every other session of the user, in case
// someone had signed in with the previous password. The current session stays active.
func (s *Service) changePassword(w http.ResponseWriter, r *http.Request) error {
	var in changePasswordInput
	if err := httpx.Decode(w, r, &in); err != nil {
		return err
	}

	fields := httpx.Fields{}
	if in.CurrentPassword == "" {
		fields["current_password"] = i18n.M("validation.required")
	}
	if msg := validatePassword(in.Password); !msg.IsZero() {
		fields["password"] = msg
	}
	if len(fields) > 0 {
		return httpx.Validation(fields)
	}

	user := MustUser(r.Context())
	if err := s.checkPassword(r.Context(), user.ID, in.CurrentPassword, "current_password"); err != nil {
		return err
	}

	// The hash (argon2, slow) is computed before opening the transaction, so a database connection
	// is not held while it waits for its turn.
	hash, err := HashPassword(r.Context(), in.Password)
	if err != nil {
		return fmt.Errorf("hash the password: %w", err)
	}

	// The new password and closing the other sessions go together: either both happen, or neither.
	var keep string
	err = pgx.BeginFunc(r.Context(), s.db, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		if err := savePassword(r.Context(), q, user.ID, hash); err != nil {
			return err
		}
		var err error
		keep, err = s.deleteOtherSessions(r, q, user.ID)
		return err
	})
	if err != nil {
		return err
	}
	s.sessionsEnded(r.Context(), user.ID, keep)
	s.notify(r.Context(), user.ID, NotifyPasswordChanged, nil)

	httpx.Write(w, http.StatusNoContent, nil)
	return nil
}
