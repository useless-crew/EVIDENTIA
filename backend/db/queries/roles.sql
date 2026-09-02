-- Evidentia — Role & User-Role Assignment Queries

-- name: ListRoles :many
SELECT id, name, description, created_at
FROM roles
ORDER BY name;

-- name: GetRoleByID :one
SELECT id, name, description, created_at
FROM roles
WHERE id = $1;

-- name: GetRoleByName :one
SELECT id, name, description, created_at
FROM roles
WHERE name = $1;

-- name: CreateRole :one
INSERT INTO roles (name, description)
VALUES ($1, $2)
ON CONFLICT ON CONSTRAINT roles_name_unique DO NOTHING
RETURNING id, name, description, created_at;

-- name: AssignRoleToUser :exec
INSERT INTO user_roles (user_id, role_id)
VALUES ($1, $2)
ON CONFLICT ON CONSTRAINT user_roles_user_role_unique DO NOTHING;

-- name: RemoveRoleFromUser :exec
DELETE FROM user_roles
WHERE user_id = $1 AND role_id = $2;

-- name: ListRolesForUser :many
SELECT r.id, r.name, r.description, r.created_at
FROM roles r
JOIN user_roles ur ON ur.role_id = r.id
WHERE ur.user_id = $1
ORDER BY r.name;

-- name: ListUserIDsForRole :many
SELECT ur.user_id
FROM user_roles ur
WHERE ur.role_id = $1;
