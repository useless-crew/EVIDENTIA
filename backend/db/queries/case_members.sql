-- Evidentia — Case Membership Queries
--
-- See case_members_select's policy comment in the migration: a caller can
-- only ever see their OWN membership row through this table (not their
-- co-members'), except as ADMIN. ListCaseMembers therefore returns, for a
-- non-admin caller, at most the one row describing their own membership —
-- this is a deliberate System 2 scope limit, not a bug in this query.

-- name: AddCaseMember :one
INSERT INTO case_members (case_id, user_id, membership_type, added_by)
VALUES ($1, $2, $3, $4)
RETURNING id, case_id, user_id, membership_type, added_by, created_at, removed_at;

-- name: RemoveCaseMember :exec
UPDATE case_members
SET removed_at = now()
WHERE case_id = $1 AND user_id = $2 AND removed_at IS NULL;

-- name: GetActiveCaseMembership :one
SELECT id, case_id, user_id, membership_type, added_by, created_at, removed_at
FROM case_members
WHERE case_id = $1 AND user_id = $2 AND removed_at IS NULL;

-- name: ListCaseMembers :many
SELECT id, case_id, user_id, membership_type, added_by, created_at, removed_at
FROM case_members
WHERE case_id = $1 AND removed_at IS NULL
ORDER BY created_at;

-- name: ListActiveCasesForUser :many
SELECT c.id, c.case_number, c.title, c.status, c.created_at
FROM cases c
JOIN case_members cm ON cm.case_id = c.id
WHERE cm.user_id = $1 AND cm.removed_at IS NULL
ORDER BY c.created_at DESC;
