package auth

import (
	"errors"
	"fmt"
	"net/http"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/NoeOnDev/go-api-base/internal/db"
	"github.com/NoeOnDev/go-api-base/internal/httpx"
	"github.com/NoeOnDev/go-api-base/internal/i18n"
	"github.com/NoeOnDev/go-api-base/internal/store"
	"github.com/NoeOnDev/go-api-base/internal/validate"
)

// Roles an administrator creates, each with its permissions: the equivalent of the roles of
// spatie/laravel-permission. See "Roles" in docs/auth.md. The permissions themselves are defined by
// the code (permissions.go); here an administrator only decides which role has which.

// Role is a role as the API returns it.
type Role struct {
	Key  string `json:"key"`
	Name string `json:"name"`
	// BuiltIn marks user and admin, which come with the template and cannot be changed.
	BuiltIn bool `json:"built_in"`
	// Permissions are the ones the role has. For admin, every one the code defines.
	Permissions []string `json:"permissions"`
	// Users is how many accounts have the role.
	Users     int64     `json:"users"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// maxRoleNameLength is the longest name of a role. The key has the limit of its column.
const (
	maxRoleKeyLength  = 40
	maxRoleNameLength = 100
)

var (
	errRoleNotFound = httpx.NotFound("role_not_found")
	errRoleTaken    = httpx.Conflict("role_taken", "key")
	errRoleInUse    = httpx.Conflict("role_in_use", "")
	errBuiltInRole  = httpx.Conflict("built_in_role", "")
)

// toRole turns a row into what the API returns. The permissions the code no longer defines are
// left out: they do nothing.
func (s *Service) toRole(r store.ListRolesRow) Role {
	role := Role{Key: r.Key, Name: r.Name, BuiltIn: r.BuiltIn, Users: r.Users, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt}
	if r.Key == RoleAdmin {
		role.Permissions = s.Permissions()
		return role
	}
	role.Permissions = []string{}
	for _, p := range r.Permissions {
		if s.defined(p) {
			role.Permissions = append(role.Permissions, p)
		}
	}
	return role
}

// listPermissions answers GET /admin/permissions: the permissions a role can have, which are the
// ones the code defines.
func (s *Service) listPermissions(w http.ResponseWriter, r *http.Request) error {
	httpx.Write(w, http.StatusOK, map[string][]string{"data": s.Permissions()})
	return nil
}

// listRoles answers GET /admin/roles: every role, with its permissions and how many users have it.
func (s *Service) listRoles(w http.ResponseWriter, r *http.Request) error {
	rows, err := s.q.ListRoles(r.Context())
	if err != nil {
		return fmt.Errorf("list roles: %w", err)
	}
	data := make([]Role, len(rows))
	for i, row := range rows {
		data[i] = s.toRole(row)
	}
	httpx.Write(w, http.StatusOK, map[string][]Role{"data": data})
	return nil
}

// writeRole answers with the role as it is stored.
func (s *Service) writeRole(w http.ResponseWriter, r *http.Request, status int, key string) error {
	row, err := s.q.GetRole(r.Context(), key)
	if errors.Is(err, pgx.ErrNoRows) {
		return errRoleNotFound
	}
	if err != nil {
		return fmt.Errorf("get role %s: %w", key, err)
	}
	httpx.Write(w, status, s.toRole(store.ListRolesRow(row)))
	return nil
}

type roleDefinition struct {
	Key         string   `json:"key"`
	Name        string   `json:"name"`
	Permissions []string `json:"permissions"`
	// Password is the administrator's, when the permissions of an existing role change.
	Password string `json:"password"`
}

// validate checks the name and the permissions, and the key when the role is new.
func (s *Service) validateRole(in *roleDefinition, withKey bool) error {
	fields := httpx.Fields{}
	if withKey {
		// The format of the column: lowercase letters, digits and "_", starting with a letter.
		if msg := validate.Required(in.Key, maxRoleKeyLength); !msg.IsZero() {
			fields["key"] = msg
		} else if !roleKeyPattern.MatchString(in.Key) {
			fields["key"] = i18n.M("validation.role_key")
		}
	}
	if msg := validate.Required(in.Name, maxRoleNameLength); !msg.IsZero() {
		fields["name"] = msg
	}
	slices.Sort(in.Permissions)
	in.Permissions = slices.Compact(in.Permissions)
	for _, p := range in.Permissions {
		if !s.defined(p) {
			fields["permissions"] = i18n.M("validation.unknown_permission", "permission", p)
			break
		}
	}
	if len(fields) > 0 {
		return httpx.Validation(fields)
	}
	return nil
}

// createRole answers POST /admin/roles with {key, name, permissions}. A new role has no users, so
// it gives nothing to anyone yet: assigning it does (PUT /admin/users/{id}/role).
func (s *Service) createRole(w http.ResponseWriter, r *http.Request) error {
	var in roleDefinition
	if err := httpx.Decode(w, r, &in); err != nil {
		return err
	}
	if err := s.validateRole(&in, true); err != nil {
		return err
	}

	err := pgx.BeginFunc(r.Context(), s.db, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		err := q.CreateRole(r.Context(), store.CreateRoleParams{Key: in.Key, Name: in.Name})
		if _, ok := db.IsUniqueViolation(err); ok {
			return errRoleTaken
		}
		if err != nil {
			return fmt.Errorf("create role %s: %w", in.Key, err)
		}
		if err := q.AddRolePermissions(r.Context(), store.AddRolePermissionsParams{Role: in.Key, Permissions: in.Permissions}); err != nil {
			return fmt.Errorf("permissions of role %s: %w", in.Key, err)
		}
		return nil
	})
	if err != nil {
		return err
	}
	httpx.LoggerFrom(r.Context()).Info("role created", "role", in.Key, "permissions", in.Permissions)

	w.Header().Set("Location", "/admin/roles/"+in.Key)
	return s.writeRole(w, r, http.StatusCreated, in.Key)
}

// updateRole answers PUT /admin/roles/{key} with {name, permissions, password}: it replaces the name
// and the permissions. It asks for the administrator's password, like changing the role of a user:
// it changes what every user of the role can do, from their next request.
func (s *Service) updateRole(w http.ResponseWriter, r *http.Request) error {
	key := r.PathValue("key")
	var in roleDefinition
	if err := httpx.Decode(w, r, &in); err != nil {
		return err
	}
	if err := s.validateRole(&in, false); err != nil {
		return err
	}
	if key == RoleAdmin || key == RoleUser {
		return errBuiltInRole
	}
	admin := MustUser(r.Context())
	if err := s.checkPassword(r.Context(), admin.ID, in.Password, "password"); err != nil {
		return err
	}

	var users []int64
	err := pgx.BeginFunc(r.Context(), s.db, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		rows, err := q.UpdateRole(r.Context(), store.UpdateRoleParams{Key: key, Name: in.Name})
		if err != nil {
			return fmt.Errorf("update role %s: %w", key, err)
		}
		if rows == 0 {
			return errRoleNotFound
		}
		if err := q.DeleteRolePermissions(r.Context(), key); err != nil {
			return fmt.Errorf("permissions of role %s: %w", key, err)
		}
		if err := q.AddRolePermissions(r.Context(), store.AddRolePermissionsParams{Role: key, Permissions: in.Permissions}); err != nil {
			return fmt.Errorf("permissions of role %s: %w", key, err)
		}
		if users, err = q.ListRoleUserIDs(r.Context(), key); err != nil {
			return fmt.Errorf("users of role %s: %w", key, err)
		}
		return nil
	})
	if err != nil {
		return err
	}
	// Their streams are cut, so they open again with what the role allows now (the topics).
	for _, id := range users {
		s.sessionsEnded(r.Context(), id, "")
	}
	httpx.LoggerFrom(r.Context()).Info("role changed", "role", key, "permissions", in.Permissions, "users", len(users))

	return s.writeRole(w, r, http.StatusOK, key)
}

// deleteRole answers DELETE /admin/roles/{key}. A role that users have answers 409 role_in_use:
// give them another one first.
func (s *Service) deleteRole(w http.ResponseWriter, r *http.Request) error {
	key := r.PathValue("key")
	if key == RoleAdmin || key == RoleUser {
		return errBuiltInRole
	}
	rows, err := s.q.DeleteRole(r.Context(), key)
	if _, ok := db.IsForeignKeyViolation(err); ok {
		return errRoleInUse
	}
	if err != nil {
		return fmt.Errorf("delete role %s: %w", key, err)
	}
	if rows == 0 {
		return errRoleNotFound
	}
	httpx.LoggerFrom(r.Context()).Info("role deleted", "role", key)

	httpx.Write(w, http.StatusNoContent, nil)
	return nil
}
