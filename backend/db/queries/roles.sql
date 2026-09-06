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

-- name: AdminUserExists :one
-- Used only by internal/bootstrap to decide whether the initial-admin
-- bootstrap has already run — true the moment any user holds the ADMIN
-- role, regardless of how they came to hold it.
SELECT EXISTS (
    SELECT 1 FROM user_roles ur
    JOIN roles r ON r.id = ur.role_id
    WHERE r.name = 'ADMIN'
) AS exists;

-- name: AcquireAdminGuardLock :exec
-- System 14's "last active Administrator" safeguard
-- (internal/service.UserService.ensureNotLastActiveAdmin) — a
-- PostgreSQL transaction-scoped advisory lock (automatically released at
-- COMMIT/ROLLBACK, never leaked across a pooled connection), acquired
-- BEFORE CountActiveUsersWithRole below and held through the role/status
-- UPDATE that follows it in the SAME transaction. This is what makes the
-- guard correct under concurrency, not merely "correct for one caller at
-- a time": two admins concurrently demoting/deactivating two DIFFERENT
-- remaining admins would otherwise each independently observe "2 active
-- admins, safe to proceed" and both commit, leaving zero — this lock
-- serializes any such pair of operations so the second one to run
-- re-counts AFTER the first has already committed. The key is a fixed,
-- arbitrary constant distinct from internal/audit's own
-- auditChainLockKey (see that package's identical idiom) — it names no
-- row and touches no other lock table.
SELECT pg_advisory_xact_lock(sqlc.arg(lock_key)::bigint);

-- name: CountActiveUsersWithRole :one
-- The count internal/service.UserService.ensureNotLastActiveAdmin reads
-- (only after AcquireAdminGuardLock above) to decide whether a
-- role/status change may proceed — ACTIVE users only, since an already
-- INACTIVE/SUSPENDED admin was never a "usable" administrator to begin
-- with and removing THEIR admin status/further deactivating them cannot
-- newly cause a lockout. Note users.status's own established convention
-- is lowercase ('active'/'inactive'/'suspended' — see
-- users_status_check, models.UserStatusActive), unlike some other
-- status-bearing tables in this schema — matched exactly here, not
-- assumed.
SELECT count(*) FROM users u
JOIN user_roles ur ON ur.user_id = u.id
WHERE ur.role_id = $1 AND u.status = 'active';
