-- ListRoles returns every role with its permissions and how many users have it: the two of the
-- template first, then the others by name.
-- name: ListRoles :many
SELECT r.key, r.name, r.built_in, r.created_at, r.updated_at,
       ARRAY(SELECT p.permission FROM role_permissions p WHERE p.role = r.key ORDER BY p.permission)::text[] AS permissions,
       (SELECT count(*) FROM users u WHERE u.role = r.key) AS users
FROM roles r
ORDER BY r.built_in DESC, r.name, r.key;

-- name: GetRole :one
SELECT r.key, r.name, r.built_in, r.created_at, r.updated_at,
       ARRAY(SELECT p.permission FROM role_permissions p WHERE p.role = r.key ORDER BY p.permission)::text[] AS permissions,
       (SELECT count(*) FROM users u WHERE u.role = r.key) AS users
FROM roles r
WHERE r.key = $1;

-- ListRolePermissions returns the permissions of a role, for a user that does not come from a
-- session (the response of the login).
-- name: ListRolePermissions :many
SELECT permission FROM role_permissions
WHERE role = $1;

-- name: CreateRole :exec
INSERT INTO roles (key, name)
VALUES ($1, $2);

-- UpdateRole renames a role an administrator created, and returns whether it found it. The two of
-- the template are not changed.
-- name: UpdateRole :execrows
UPDATE roles
SET name = $2, updated_at = now()
WHERE key = $1 AND NOT built_in;

-- DeleteRolePermissions takes the given permissions off a role. They are the ones the code defines:
-- one it no longer defines (a deploy that went back, a resource removed for a while) stays stored.
-- name: DeleteRolePermissions :exec
DELETE FROM role_permissions
WHERE role = sqlc.arg('role') AND permission = ANY(sqlc.arg('permissions')::text[]);

-- name: AddRolePermissions :exec
INSERT INTO role_permissions (role, permission)
SELECT sqlc.arg('role'), unnest(sqlc.arg('permissions')::text[]);

-- DeleteRole deletes a role an administrator created, and returns whether it found it. One that
-- users have fails with a foreign key violation.
-- name: DeleteRole :execrows
DELETE FROM roles
WHERE key = $1 AND NOT built_in;

-- ListRoleUserIDs returns the users of a role, to cut their streams when its permissions change.
-- name: ListRoleUserIDs :many
SELECT id FROM users
WHERE role = $1;
