package auth

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"sync"

	"github.com/NoeOnDev/go-api-base/internal/httpx"
)

// Permissions: what a role lets its users do, beyond having a session. See "Roles" in docs/auth.md.
//
// A permission is a name the code defines, like "products.manage": the resource it is about and the
// action. The routes ask for it with RequirePermission, and a handler with User.Can. Which roles
// have it is data: an administrator decides it from /admin/roles (roles.go). The admin role has
// every permission, and the user role none.

// permissionPattern is the format of a permission: lowercase words joined by dots.
var permissionPattern = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)+$`)

// permissionSet are the permissions the code defined, which are the ones a role can be given.
type permissionSet struct {
	mu   sync.RWMutex
	keys map[string]struct{}
}

// DefinePermissions makes the permissions known, so a role can have them. RequirePermission already
// does it for the one it asks for: call this for a permission that is only checked inside a handler
// (User.Can). A name in another format is a programming error, and it panics at startup.
func (s *Service) DefinePermissions(permissions ...string) {
	s.permissions.mu.Lock()
	defer s.permissions.mu.Unlock()
	if s.permissions.keys == nil {
		s.permissions.keys = map[string]struct{}{}
	}
	for _, p := range permissions {
		if !permissionPattern.MatchString(p) {
			panic(fmt.Sprintf("auth: %q is not a permission name (resource.action, like products.manage)", p))
		}
		s.permissions.keys[p] = struct{}{}
	}
}

// Permissions returns the permissions the code defined, in order.
func (s *Service) Permissions() []string {
	s.permissions.mu.RLock()
	defer s.permissions.mu.RUnlock()
	keys := make([]string, 0, len(s.permissions.keys))
	for p := range s.permissions.keys {
		keys = append(keys, p)
	}
	slices.Sort(keys)
	return keys
}

// defined reports whether the code defined the permission.
func (s *Service) defined(permission string) bool {
	s.permissions.mu.RLock()
	defer s.permissions.mu.RUnlock()
	_, ok := s.permissions.keys[permission]
	return ok
}

// RequirePermission lets through only the users whose role has the permission, and the
// administrators: 401 without a session, 403 without the permission. It also defines the permission
// (see DefinePermissions).
//
//	mux.Handle("POST /products", authn.RequirePermission(PermManage, httpx.Handle(h.create)))
//
// With RequireAdminTwoFactor, an admin without two-factor authentication gets 403
// admin_two_factor_required, as in RequireAdmin.
func (s *Service) RequirePermission(permission string, next http.Handler) http.Handler {
	s.DefinePermissions(permission)
	return s.RequireUser(httpx.Handle(func(w http.ResponseWriter, r *http.Request) error {
		user := MustUser(r.Context())
		if user.adminNeedsTwoFactor {
			return errAdminTwoFactorRequired
		}
		if !user.Can(permission) {
			return httpx.Forbidden("forbidden")
		}
		next.ServeHTTP(w, r)
		return nil
	}))
}

// Can reports whether the user has the permission: their role has it, or they are an administrator
// (see IsAdmin). Use it in a handler that shows or does more for some users, like a public list
// that includes the inactive products for whoever manages them.
func (u User) Can(permission string) bool {
	return u.IsAdmin() || slices.Contains(u.Permissions, permission)
}

// grant sets what the user can do, from the permissions of their role: whether an admin still has
// to turn on two-factor authentication, and the permissions the API returns. An admin has all the
// defined ones; another role, those of its list that the code still defines.
func (s *Service) grant(u *User, rolePermissions []string) {
	u.adminNeedsTwoFactor = s.opts.RequireAdminTwoFactor && u.Role == RoleAdmin && !u.TwoFactorEnabled
	u.Permissions = []string{}
	switch {
	case u.Role == RoleAdmin && !u.adminNeedsTwoFactor:
		u.Permissions = s.Permissions()
	case u.Role != RoleAdmin:
		for _, p := range rolePermissions {
			if s.defined(p) {
				u.Permissions = append(u.Permissions, p)
			}
		}
		slices.Sort(u.Permissions)
	}
}

// withPermissions returns the user with what they can do, reading the permissions of their role.
// It is for a user that does not come from a session, which already brings them: the response of
// the login, of the registration or of the profile.
func (s *Service) withPermissions(ctx context.Context, u User) (User, error) {
	var rolePermissions []string
	if u.Role != RoleAdmin && u.Role != RoleUser {
		var err error
		if rolePermissions, err = s.q.ListRolePermissions(ctx, u.Role); err != nil {
			return User{}, fmt.Errorf("permissions of the role %s: %w", u.Role, err)
		}
	}
	s.grant(&u, rolePermissions)
	return u, nil
}
