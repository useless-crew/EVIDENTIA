-- Evidentia — Permission & Role-Permission Queries

-- name: ListPermissions :many
SELECT id, name, description, resource, action, created_at
FROM permissions
ORDER BY resource, action;

-- name: GetPermissionByID :one
SELECT id, name, description, resource, action, created_at
FROM permissions
WHERE id = $1;

-- name: GetPermissionByName :one
SELECT id, name, description, resource, action, created_at
FROM permissions
WHERE name = $1;

-- name: CreatePermission :one
INSERT INTO permissions (name, description, resource, action)
VALUES ($1, $2, $3, $4)
ON CONFLICT ON CONSTRAINT permissions_name_unique DO NOTHING
RETURNING id, name, description, resource, action, created_at;

-- name: AssignPermissionToRole :exec
INSERT INTO role_permissions (role_id, permission_id)
VALUES ($1, $2)
ON CONFLICT ON CONSTRAINT role_permissions_role_permission_unique DO NOTHING;

-- name: RemovePermissionFromRole :exec
DELETE FROM role_permissions
WHERE role_id = $1 AND permission_id = $2;

-- name: ListPermissionsForRole :many
SELECT p.id, p.name, p.description, p.resource, p.action, p.created_at
FROM permissions p
JOIN role_permissions rp ON rp.permission_id = p.id
WHERE rp.role_id = $1
ORDER BY p.resource, p.action;
