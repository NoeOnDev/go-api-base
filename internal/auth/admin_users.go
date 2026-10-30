package auth

import (
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/NoeOnDev/go-api-base/internal/db"
	"github.com/NoeOnDev/go-api-base/internal/httpx"
	"github.com/NoeOnDev/go-api-base/internal/i18n"
	"github.com/NoeOnDev/go-api-base/internal/store"
	"github.com/NoeOnDev/go-api-base/internal/validate"
)

// User management for administrators: list, see, change the role, suspend and delete. See
// "Managing users" in docs/auth.md.

// Statuses of an account, as the administrators see them.
const (
	StatusActive    = "active"
	StatusSuspended = "suspended"
)

var (
	roles    = []string{RoleUser, RoleAdmin}
	statuses = []string{StatusActive, StatusSuspended}
)

// errOwnAccount: an admin does not manage their own account from here, but from /auth/me, which
// asks for what each change needs (and does not let them suspend themselves).
var errOwnAccount = httpx.Conflict("own_account", "")

// AdminUser is a user as the administrators see them: the account and its status.
type AdminUser struct {
	User
	Status      string     `json:"status"`
	SuspendedAt *time.Time `json:"suspended_at"`
}

func toAdminUser(u store.User) AdminUser {
	status := StatusActive
	if u.SuspendedAt != nil {
		status = StatusSuspended
	}
	return AdminUser{User: toUser(u), Status: status, SuspendedAt: u.SuspendedAt}
}

// userFilters are the optional filters of the list: ?q=alice&role=admin&status=suspended.
// A nil field means "no filter".
type userFilters struct {
	Search *string
	Role   *string
	Status *string
}

func parseUserFilters(r *http.Request) (userFilters, error) {
	var f userFilters
	fields := httpx.Fields{}
	query := r.URL.Query()

	if v := strings.TrimSpace(query.Get("q")); v != "" {
		if msg := validate.MaxLen(v, 100); !msg.IsZero() {
			fields["q"] = msg
		}
		escaped := db.EscapeLike(v)
		f.Search = &escaped
	}
	if v := query.Get("role"); v != "" {
		if msg := oneOf(v, roles); !msg.IsZero() {
			fields["role"] = msg
		}
		f.Role = &v
	}
	if v := query.Get("status"); v != "" {
		if msg := oneOf(v, statuses); !msg.IsZero() {
			fields["status"] = msg
		}
		f.Status = &v
	}

	if len(fields) > 0 {
		return userFilters{}, httpx.Validation(fields)
	}
	return f, nil
}

// oneOf requires value to be one of the allowed values.
func oneOf(value string, allowed []string) i18n.Message {
	if !slices.Contains(allowed, value) {
		return i18n.M("validation.one_of", "values", strings.Join(allowed, ", "))
	}
	return i18n.Message{}
}

// listUsers answers GET /admin/users?q=&role=&status=&limit=20&offset=0, from the newest.
func (s *Service) listUsers(w http.ResponseWriter, r *http.Request) error {
	page, err := httpx.ParsePage(r)
	if err != nil {
		return err
	}
	f, err := parseUserFilters(r)
	if err != nil {
		return err
	}

	rows, err := s.q.ListUsers(r.Context(), store.ListUsersParams{
		Search: f.Search, Role: f.Role, Status: f.Status, Limit: page.Limit, Offset: page.Offset,
	})
	if err != nil {
		return fmt.Errorf("list users: %w", err)
	}
	total, err := s.q.CountUsers(r.Context(), store.CountUsersParams{Search: f.Search, Role: f.Role, Status: f.Status})
	if err != nil {
		return fmt.Errorf("count users: %w", err)
	}

	data := make([]AdminUser, len(rows))
	for i, u := range rows {
		data[i] = toAdminUser(u)
	}
	httpx.Write(w, http.StatusOK, httpx.List[AdminUser]{
		Data: data,
		Meta: httpx.PageMeta{Limit: page.Limit, Offset: page.Offset, Total: total},
	})
	return nil
}

// getUser answers GET /admin/users/{id}.
func (s *Service) getUser(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.PathID(r, "id")
	if err != nil {
		return err
	}
	return s.writeAdminUser(w, r, id)
}

// writeAdminUser answers 200 with the user as the administrators see them, or 404.
func (s *Service) writeAdminUser(w http.ResponseWriter, r *http.Request, id int64) error {
	u, err := s.q.GetUserByID(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		return errUserNotFound
	}
	if err != nil {
		return fmt.Errorf("find user %d: %w", id, err)
	}
	httpx.Write(w, http.StatusOK, toAdminUser(u))
	return nil
}

// targetUser reads the {id} of the route and rejects the admin's own account.
func targetUser(r *http.Request) (int64, error) {
	id, err := httpx.PathID(r, "id")
	if err != nil {
		return 0, err
	}
	if id == MustUser(r.Context()).ID {
		return 0, errOwnAccount
	}
	return id, nil
}

type roleInput struct {
	Role     string `json:"role"`
	Password string `json:"password"`
}

// changeRole answers PUT /admin/users/{id}/role with {role, password}. It asks for the admin's
// password: with a stolen session, making another account an admin would leave the attacker with
// access even after the session is closed.
func (s *Service) changeRole(w http.ResponseWriter, r *http.Request) error {
	id, err := targetUser(r)
	if err != nil {
		return err
	}
	var in roleInput
	if err := httpx.Decode(w, r, &in); err != nil {
		return err
	}
	if msg := oneOf(in.Role, roles); !msg.IsZero() {
		return httpx.Validation(httpx.Fields{"role": msg})
	}
	admin := MustUser(r.Context())
	if err := s.checkPassword(r.Context(), admin.ID, in.Password, "password"); err != nil {
		return err
	}

	err = pgx.BeginFunc(r.Context(), s.db, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		if in.Role != RoleAdmin {
			if err := checkNotLastAdmin(r.Context(), q, id); err != nil {
				return err
			}
		}
		rows, err := q.UpdateUserRole(r.Context(), store.UpdateUserRoleParams{ID: id, Role: in.Role})
		if err != nil {
			return fmt.Errorf("change the role of user %d: %w", id, err)
		}
		if rows == 0 {
			return errUserNotFound
		}
		return nil
	})
	if err != nil {
		return err
	}
	// The topics of the open streams were authorized with the previous role: they are cut, and the
	// browser reconnects and is authorized again with the new one. The sessions keep working.
	s.sessionsEnded(r.Context(), id, "")
	httpx.LoggerFrom(r.Context()).Info("user role changed", "target_user_id", id, "role", in.Role)

	return s.writeAdminUser(w, r, id)
}

// suspendUser answers POST /admin/users/{id}/suspend: the account cannot log in, and its
// sessions, streams and pending links are cut. It can be undone with unsuspend, so it does not
// ask for the password: suspending is the safe choice before deleting.
func (s *Service) suspendUser(w http.ResponseWriter, r *http.Request) error {
	id, err := targetUser(r)
	if err != nil {
		return err
	}

	err = pgx.BeginFunc(r.Context(), s.db, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		if err := checkNotLastAdmin(r.Context(), q, id); err != nil {
			return err
		}
		rows, err := q.SuspendUser(r.Context(), id)
		if err != nil {
			return fmt.Errorf("suspend user %d: %w", id, err)
		}
		if rows == 0 {
			return errUserNotFound
		}
		if err := q.DeleteUserSessions(r.Context(), id); err != nil {
			return fmt.Errorf("close the sessions: %w", err)
		}
		// The links and the two-factor challenge too: none of them must let them back in.
		if err := q.DeleteAllUserTokens(r.Context(), id); err != nil {
			return fmt.Errorf("delete the pending tokens: %w", err)
		}
		return nil
	})
	if err != nil {
		return err
	}
	s.sessionsEnded(r.Context(), id, "")
	httpx.LoggerFrom(r.Context()).Info("user suspended", "target_user_id", id)

	return s.writeAdminUser(w, r, id)
}

// unsuspendUser answers POST /admin/users/{id}/unsuspend: the account can log in again.
func (s *Service) unsuspendUser(w http.ResponseWriter, r *http.Request) error {
	id, err := targetUser(r)
	if err != nil {
		return err
	}
	rows, err := s.q.UnsuspendUser(r.Context(), id)
	if err != nil {
		return fmt.Errorf("unsuspend user %d: %w", id, err)
	}
	if rows == 0 {
		return errUserNotFound
	}
	httpx.LoggerFrom(r.Context()).Info("user unsuspended", "target_user_id", id)

	return s.writeAdminUser(w, r, id)
}

// deleteUserByAdmin answers DELETE /admin/users/{id} with {password}: it deletes the account
// and all its data, like DELETE /auth/me. It asks for the admin's password because it cannot be
// undone.
func (s *Service) deleteUserByAdmin(w http.ResponseWriter, r *http.Request) error {
	id, err := targetUser(r)
	if err != nil {
		return err
	}
	var in passwordInput
	if err := httpx.Decode(w, r, &in); err != nil {
		return err
	}
	admin := MustUser(r.Context())
	if err := s.checkPassword(r.Context(), admin.ID, in.Password, "password"); err != nil {
		return err
	}

	if err := s.deleteUser(r.Context(), id); err != nil {
		return err
	}
	s.sessionsEnded(r.Context(), id, "")
	httpx.LoggerFrom(r.Context()).Info("user deleted by an administrator", "target_user_id", id)

	httpx.Write(w, http.StatusNoContent, nil)
	return nil
}
