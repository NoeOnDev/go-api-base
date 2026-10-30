package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/NoeOnDev/go-api-base/internal/db"
	"github.com/NoeOnDev/go-api-base/internal/httpx"
	"github.com/NoeOnDev/go-api-base/internal/ratelimit"
	"github.com/NoeOnDev/go-api-base/internal/store"
	"github.com/NoeOnDev/go-api-base/internal/validate"
)

// Invitations: an administrator creates the account with the name, the email and the role, and
// the person chooses their password from the link that reaches them by email. Until then the
// user has no password and cannot log in. See "Invitations" in docs/auth.md.

var errInvitationAccepted = httpx.Conflict("invitation_accepted", "")

type inviteInput struct {
	Name  string `json:"name"`
	Email string `json:"email"`
	Role  string `json:"role"`
	// Locale is the language of the invitation email and of the account; if it is missing, the
	// person's browser sets it when accepting.
	Locale *string `json:"locale"`
	// Password is the admin's own, needed only to invite another admin.
	Password string `json:"password"`
}

func (s *Service) validateInvite(in *inviteInput) error {
	in.Name = strings.TrimSpace(in.Name)
	in.Email = strings.ToLower(strings.TrimSpace(in.Email))

	fields := httpx.Fields{}
	if msg := validate.Required(in.Name, maxNameLen); !msg.IsZero() {
		fields["name"] = msg
	}
	if msg := validateEmail(in.Email); !msg.IsZero() {
		fields["email"] = msg
	}
	if msg := oneOf(in.Role, roles); !msg.IsZero() {
		fields["role"] = msg
	}
	if in.Locale != nil {
		locale, msg := s.lookupLocale(*in.Locale)
		if !msg.IsZero() {
			fields["locale"] = msg
		}
		in.Locale = &locale
	}
	if len(fields) > 0 {
		return httpx.Validation(fields)
	}
	return nil
}

// inviteUser returns the handler of POST /admin/users with {name, email, role, locale?, password?}:
// it creates the user without a password and sends them the invitation, valid for 7 days.
//
// Inviting another admin asks for the admin's password, like changing a role: with a stolen
// session, a new admin would outlast it. That check has the per-user limit of the account actions
// (account, the same as the route middleware); inviting users does not, so several can be invited
// in a row.
func (s *Service) inviteUser(account *ratelimit.Limiter) func(http.ResponseWriter, *http.Request) error {
	return func(w http.ResponseWriter, r *http.Request) error {
		var in inviteInput
		if err := httpx.Decode(w, r, &in); err != nil {
			return err
		}
		if err := s.validateInvite(&in); err != nil {
			return err
		}

		admin := MustUser(r.Context())
		if in.Role == RoleAdmin {
			allowed, err := account.Allow(r.Context(), strconv.FormatInt(admin.ID, 10))
			if err != nil {
				return err
			}
			if !allowed {
				w.Header().Set("Retry-After", account.RetryAfter())
				return httpx.TooManyRequests()
			}
			if err := s.checkPassword(r.Context(), admin.ID, in.Password, "password"); err != nil {
				return err
			}
		}

		// Without a password and with the email not verified: the invitation link verifies it.
		u, err := s.q.CreateUser(r.Context(), store.CreateUserParams{
			Name:   in.Name,
			Email:  in.Email,
			Role:   in.Role,
			Locale: in.Locale,
		})
		if _, ok := db.IsUniqueViolation(err); ok {
			return httpx.Conflict("email_taken", "email")
		}
		if err != nil {
			return fmt.Errorf("create the invited user: %w", err)
		}
		httpx.LoggerFrom(r.Context()).Info("user invited", "target_user_id", u.ID, "role", u.Role)

		// As in the registration: if the email cannot be queued, the user exists anyway, and the
		// invitation can be sent again with POST /admin/users/{id}/invitation.
		if err := s.sendInvitation(r.Context(), u, admin); err != nil {
			httpx.LoggerFrom(r.Context()).Error("could not send the invitation", "target_user_id", u.ID, "error", err)
		}

		httpx.Write(w, http.StatusCreated, toAdminUser(u))
		return nil
	}
}

// resendInvitation answers POST /admin/users/{id}/invitation: a new link, and the previous one
// stops working. It has the per-recipient email limit.
func (s *Service) resendInvitation(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.PathID(r, "id")
	if err != nil {
		return err
	}
	u, err := s.q.GetUserByID(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		return errUserNotFound
	}
	if err != nil {
		return fmt.Errorf("find user %d: %w", id, err)
	}
	if u.PasswordHash != nil {
		return errInvitationAccepted
	}

	allowed, err := s.allowEmail(r.Context(), purposeInvitation, u.ID)
	if err != nil {
		return err
	}
	if !allowed {
		return errTooManyEmails
	}
	if err := s.sendInvitation(r.Context(), u, MustUser(r.Context())); err != nil {
		return err
	}

	httpx.Write(w, http.StatusNoContent, nil)
	return nil
}

// sendInvitation issues the invitation token (only the last one works) and sends it to the user,
// in their language.
func (s *Service) sendInvitation(ctx context.Context, u store.User, admin User) error {
	token, err := s.issueToken(ctx, u.ID, purposeInvitation, invitationTTL, nil)
	if err != nil {
		return err
	}
	return s.sendEmail(ctx, u.Email, u.Locale, "invitation", emailData{
		Name:    u.Name,
		Link:    s.frontendLink("/accept-invitation", token),
		Expires: s.expiresIn(u.Locale, invitationTTL),
		App:     s.opts.Issuer,
		Inviter: admin.Name,
	})
}

type acceptInvitationInput struct {
	Token    string `json:"token"`
	Password string `json:"password"`
}

// acceptInvitation answers POST /auth/invitation/accept with the token of the link and the new
// password: it sets the password, marks the email as verified (the link reached it) and starts
// the session, like the registration. The language of the account, if the admin did not choose
// one, is the one of the browser.
func (s *Service) acceptInvitation(w http.ResponseWriter, r *http.Request) error {
	var in acceptInvitationInput
	if err := httpx.Decode(w, r, &in); err != nil {
		return err
	}

	// As in the password reset: the password is validated first, the link is checked without
	// using it up, and the hash (argon2, expensive) is computed outside the transaction.
	if msg := validatePassword(in.Password); !msg.IsZero() {
		return httpx.Validation(httpx.Fields{"password": msg})
	}
	if err := s.checkToken(r.Context(), in.Token, purposeInvitation); err != nil {
		return err
	}
	hash, err := HashPassword(r.Context(), in.Password)
	if err != nil {
		return fmt.Errorf("hash the password: %w", err)
	}
	var locale *string
	if l, ok := s.catalog.Choose(r.Header.Get("Accept-Language")); ok {
		locale = &l
	}

	// All or nothing: if a step fails, the link still works.
	var userID int64
	err = pgx.BeginFunc(r.Context(), s.db, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		id, err := consumeToken(r.Context(), q, in.Token, purposeInvitation)
		if err != nil {
			return err
		}
		u, err := q.GetUserByID(r.Context(), id)
		if err != nil {
			return fmt.Errorf("find user %d: %w", id, err)
		}
		if u.SuspendedAt != nil {
			return errAccountSuspended
		}
		rows, err := q.SetInitialPassword(r.Context(), store.SetInitialPasswordParams{ID: id, PasswordHash: &hash, Locale: locale})
		if err != nil {
			return fmt.Errorf("set the password of user %d: %w", id, err)
		}
		if rows == 0 {
			// It already has a password (it accepted, or set one with "forgot my password"): the
			// old link must not overwrite it.
			return errInvalidToken
		}
		userID = id
		return nil
	})
	if err != nil {
		return err
	}

	u, err := s.q.GetUserByID(r.Context(), userID)
	if err != nil {
		return fmt.Errorf("find user %d: %w", userID, err)
	}
	if err := s.StartSession(w, r, userID); err != nil {
		return err
	}
	httpx.LoggerFrom(r.Context()).Info("invitation accepted", "user_id", userID)

	httpx.Write(w, http.StatusOK, toUser(u))
	return nil
}
