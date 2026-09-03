-- Evidentia — Case Queries
--
-- Every query here runs through the RLS-bound application role
-- (evidentia_app): row visibility is enforced by the cases_select/
-- cases_insert/cases_update policies (backend/db/migrations), not by
-- anything in this file. A query with no WHERE clause restricting to the
-- caller is not a bug — RLS applies regardless.

-- name: CreateCase :one
INSERT INTO cases (case_number, title, description, created_by, metadata)
VALUES ($1, $2, $3, $4, $5)
RETURNING id, case_number, title, description, status, metadata, created_by, created_at, updated_at;

-- name: GetCaseByID :one
SELECT id, case_number, title, description, status, metadata, created_by, created_at, updated_at
FROM cases
WHERE id = $1;

-- name: GetCaseByCaseNumber :one
SELECT id, case_number, title, description, status, metadata, created_by, created_at, updated_at
FROM cases
WHERE case_number = $1;

-- name: ListCases :many
SELECT id, case_number, title, description, status, metadata, created_by, created_at, updated_at
FROM cases
ORDER BY created_at DESC
LIMIT $1 OFFSET $2;

-- name: ListCasesByStatus :many
SELECT id, case_number, title, description, status, metadata, created_by, created_at, updated_at
FROM cases
WHERE status = $1
ORDER BY created_at DESC
LIMIT $2 OFFSET $3;

-- name: CountCases :one
SELECT count(*) FROM cases;

-- name: ListCasesFiltered :many
-- Every filter is optional (NULL = "no constraint on this field") so a
-- single query serves GET /cases whether the caller passed zero or every
-- filter — parameterized throughout, never string-concatenated. Combined
-- with RLS (FORCE'd on this table), the WHERE clause below and the
-- caller's row-visibility policy both apply: this query can only ever
-- narrow what RLS already allows, never widen it.
SELECT id, case_number, title, description, status, metadata, created_by, created_at, updated_at
FROM cases
WHERE (sqlc.narg(status)::text IS NULL OR status = sqlc.narg(status))
  AND (sqlc.narg(case_number)::text IS NULL OR case_number ILIKE '%' || sqlc.narg(case_number)::text || '%')
  AND (sqlc.narg(title)::text IS NULL OR title ILIKE '%' || sqlc.narg(title)::text || '%')
  AND (sqlc.narg(created_by)::uuid IS NULL OR created_by = sqlc.narg(created_by))
  AND (sqlc.narg(created_from)::timestamptz IS NULL OR created_at >= sqlc.narg(created_from))
  AND (sqlc.narg(created_to)::timestamptz IS NULL OR created_at < sqlc.narg(created_to))
ORDER BY created_at DESC
LIMIT sqlc.arg(limit_val) OFFSET sqlc.arg(offset_val);

-- name: CountCasesFiltered :one
-- Same filters as ListCasesFiltered — the caller's authorized, filtered
-- total for pagination metadata, not an unfiltered table count.
SELECT count(*) FROM cases
WHERE (sqlc.narg(status)::text IS NULL OR status = sqlc.narg(status))
  AND (sqlc.narg(case_number)::text IS NULL OR case_number ILIKE '%' || sqlc.narg(case_number)::text || '%')
  AND (sqlc.narg(title)::text IS NULL OR title ILIKE '%' || sqlc.narg(title)::text || '%')
  AND (sqlc.narg(created_by)::uuid IS NULL OR created_by = sqlc.narg(created_by))
  AND (sqlc.narg(created_from)::timestamptz IS NULL OR created_at >= sqlc.narg(created_from))
  AND (sqlc.narg(created_to)::timestamptz IS NULL OR created_at < sqlc.narg(created_to));

-- name: UpdateCase :one
UPDATE cases
SET
    title = $2,
    description = $3,
    status = $4,
    metadata = $5,
    updated_at = now()
WHERE id = $1
RETURNING id, case_number, title, description, status, metadata, created_by, created_at, updated_at;
