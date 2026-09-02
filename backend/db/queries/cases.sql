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
