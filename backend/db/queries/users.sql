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
