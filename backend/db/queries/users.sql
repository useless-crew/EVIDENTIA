-- Evidentia — User Queries
--
-- password_hash is exposed ONLY by GetUserByEmailForAuth, named explicitly
-- so its one legitimate caller (System 3's login flow) is unmistakable at
-- every call site. Every other query below deliberately omits it.

-- name: CreateUser :one
INSERT INTO users (
    email,
    password_hash,
    first_name,
    last_name,
    display_name,
    phone
) VALUES (
    $1, $2, $3, $4, $5, $6
)
RETURNING
    id,
    email,
    first_name,
    last_name,
    display_name,
    phone,
    status,
    created_at,
    updated_at,
    last_login_at;

-- name: GetUserByID :one
SELECT
    id,
    email,
    first_name,
    last_name,
    display_name,
    phone,
    status,
    created_at,
    updated_at,
    last_login_at
FROM users
WHERE id = $1;

-- name: GetUserByEmail :one
SELECT
    id,
    email,
    first_name,
    last_name,
    display_name,
    phone,
    status,
    created_at,
    updated_at,
    last_login_at
FROM users
WHERE email = $1;

-- name: GetUserByEmailForAuth :one
SELECT
    id,
    email,
    password_hash,
    status
FROM users
WHERE email = $1;

-- name: ListUsers :many
SELECT
    id,
    email,
    first_name,
    last_name,
    display_name,
    phone,
    status,
    created_at,
    updated_at,
    last_login_at
FROM users
ORDER BY created_at DESC
LIMIT $1 OFFSET $2;

-- name: CountUsers :one
SELECT count(*) FROM users;

-- name: UpdateUserProfile :one
UPDATE users
SET
    first_name = $2,
    last_name = $3,
    display_name = $4,
    phone = $5,
    updated_at = now()
WHERE id = $1
RETURNING
    id,
    email,
    first_name,
    last_name,
    display_name,
    phone,
    status,
    created_at,
    updated_at,
    last_login_at;

-- name: UpdateUserStatus :one
UPDATE users
SET
    status = $2,
    updated_at = now()
WHERE id = $1
RETURNING
    id,
    email,
    first_name,
    last_name,
    display_name,
    phone,
    status,
    created_at,
    updated_at,
    last_login_at;

-- name: ListUsersFiltered :many
-- Every filter is optional (NULL = "no constraint on this field") — same
-- convention as ListCasesFiltered in cases.sql. The role filter is an
-- EXISTS against user_roles/roles rather than a JOIN, so a user with
-- multiple roles is never duplicated in the result set.
SELECT
    id,
    email,
    first_name,
    last_name,
    display_name,
    phone,
    status,
    created_at,
    updated_at,
    last_login_at
FROM users u
WHERE (sqlc.narg(status)::text IS NULL OR u.status = sqlc.narg(status))
  AND (
      sqlc.narg(role)::text IS NULL
      OR EXISTS (
          SELECT 1 FROM user_roles ur
          JOIN roles r ON r.id = ur.role_id
          WHERE ur.user_id = u.id AND r.name = sqlc.narg(role)
      )
  )
  AND (
      sqlc.narg(search)::text IS NULL
      OR u.email ILIKE '%' || sqlc.narg(search)::text || '%'
      OR u.first_name ILIKE '%' || sqlc.narg(search)::text || '%'
      OR u.last_name ILIKE '%' || sqlc.narg(search)::text || '%'
      OR u.display_name ILIKE '%' || sqlc.narg(search)::text || '%'
  )
ORDER BY u.created_at DESC
LIMIT sqlc.arg(limit_val) OFFSET sqlc.arg(offset_val);

-- name: CountUsersFiltered :one
-- Same filters as ListUsersFiltered — the caller's filtered total for
-- pagination metadata, not an unfiltered table count.
SELECT count(*) FROM users u
WHERE (sqlc.narg(status)::text IS NULL OR u.status = sqlc.narg(status))
  AND (
      sqlc.narg(role)::text IS NULL
      OR EXISTS (
          SELECT 1 FROM user_roles ur
          JOIN roles r ON r.id = ur.role_id
          WHERE ur.user_id = u.id AND r.name = sqlc.narg(role)
      )
  )
  AND (
      sqlc.narg(search)::text IS NULL
      OR u.email ILIKE '%' || sqlc.narg(search)::text || '%'
      OR u.first_name ILIKE '%' || sqlc.narg(search)::text || '%'
      OR u.last_name ILIKE '%' || sqlc.narg(search)::text || '%'
      OR u.display_name ILIKE '%' || sqlc.narg(search)::text || '%'
  );

-- name: UpdateUserPasswordHash :exec
UPDATE users
SET
    password_hash = $2,
    updated_at = now()
WHERE id = $1;

-- name: UpdateUserLastLogin :exec
UPDATE users
SET last_login_at = now()
WHERE id = $1;
