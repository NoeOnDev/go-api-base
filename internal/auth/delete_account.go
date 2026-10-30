package auth

import (
	"net/http"

	"github.com/NoeOnDev/go-api-base/internal/httpx"
)

// deleteAccount answers DELETE /auth/me with {password}: it deletes the user's account and all
// its data (sessions, tokens, notes, notifications…, through ON DELETE CASCADE). It asks for the
// password, like any action that cannot be undone.
//
// It does not let the only admin be deleted, so nobody leaves the API without an administrator
// by mistake. If a project table references the user without CASCADE (with RESTRICT), the
// deletion fails and answers 409: every table with user data should use CASCADE or SET NULL.
func (s *Service) deleteAccount(w http.ResponseWriter, r *http.Request) error {
	var in passwordInput
	if err := httpx.Decode(w, r, &in); err != nil {
		return err
	}
	user := MustUser(r.Context())
	if err := s.checkPassword(r.Context(), user.ID, in.Password, "password"); err != nil {
		return err
	}

	if err := s.deleteUser(r.Context(), user.ID); err != nil {
		return err
	}

	// Their sessions no longer exist: their streams are cut and the cookie is deleted.
	s.sessionsEnded(r.Context(), user.ID, "")
	s.clearSessionCookie(w)

	// The last email, in case it was not the owner. If it is not sent, the account is still deleted.
	if err := s.sendEmail(r.Context(), user.Email, user.Locale, "account_deleted.txt", emailData{Name: user.Name}); err != nil {
		httpx.LoggerFrom(r.Context()).Error("could not send the account deleted email", "user_id", user.ID, "error", err)
	}
	httpx.LoggerFrom(r.Context()).Info("account deleted", "user_id", user.ID)

	httpx.Write(w, http.StatusNoContent, nil)
	return nil
}
