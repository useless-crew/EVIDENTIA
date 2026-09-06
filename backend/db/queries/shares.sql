-- Evidentia — Document Sharing Queries
--
-- Shares are immutable history once created: the only mutation is
-- RevokeDocumentShare's single ACTIVE -> REVOKED transition (WHERE
-- status = 'ACTIVE', so revoking an already-revoked share is a documented
-- no-op — zero rows affected, never an error). There is no
-- UpdateDocumentShare/DeleteDocumentShare query, and the runtime role
-- holds no DELETE grant on this table (see the migration).

-- name: CreateDocumentShare :one
INSERT INTO document_shares (
    id, document_id, shared_with_user_id, created_by_user_id,
    permission, expires_at, reason
) VALUES (
    $1, $2, $3, $4, $5, $6, $7
)
RETURNING id, document_id, shared_with_user_id, created_by_user_id,
    permission, status, expires_at, reason, metadata, created_at,
    revoked_at, revoked_by_user_id;

-- name: GetDocumentShareByID :one
-- Scoped by document_id as well as id — see ShareService.RevokeShare's
-- doc comment for why this is the IDOR-safety-relevant lookup (master
-- prompt §16/§50: "cannot use another share ID" against a different
-- document must not even resolve the row).
SELECT id, document_id, shared_with_user_id, created_by_user_id,
    permission, status, expires_at, reason, metadata, created_at,
    revoked_at, revoked_by_user_id
FROM document_shares
WHERE id = $1 AND document_id = $2;

-- name: ListDocumentSharesForDocument :many
SELECT id, document_id, shared_with_user_id, created_by_user_id,
    permission, status, expires_at, reason, metadata, created_at,
    revoked_at, revoked_by_user_id
FROM document_shares
WHERE document_id = $1
ORDER BY created_at DESC;

-- name: RevokeDocumentShare :one
UPDATE document_shares
SET status = 'REVOKED', revoked_at = now(), revoked_by_user_id = $3
WHERE id = $1 AND document_id = $2 AND status = 'ACTIVE'
RETURNING id, document_id, shared_with_user_id, created_by_user_id,
    permission, status, expires_at, reason, metadata, created_at,
    revoked_at, revoked_by_user_id;

-- name: GetActiveShareForDocumentAndUser :one
-- The authorization hot path (internal/authz/share_policy.go) — mirrors
-- documents_select's RLS OR-branch exactly (see the migration). Expiry is
-- evaluated in SQL (now()) rather than in Go, so it can never drift from
-- what the database itself would independently allow via RLS.
SELECT id, document_id, shared_with_user_id, created_by_user_id,
    permission, status, expires_at, reason, metadata, created_at,
    revoked_at, revoked_by_user_id
FROM document_shares
WHERE document_id = $1
  AND shared_with_user_id = $2
  AND status = 'ACTIVE'
  AND (expires_at IS NULL OR expires_at > now())
LIMIT 1;

-- name: ListSharedWithMe :many
-- Documents visibility for the "Shared With Me" view (master prompt
-- §59): every document for which the caller holds a currently-active,
-- unexpired share, newest share first. Joins into documents for display
-- metadata directly — RLS's documents_select policy already permits this
-- (see the migration's delegated-access OR-branch), so no separate
-- authorization check is needed beyond "this share row exists and is
-- valid", but ShareService still runs this under the caller's own RLS
-- identity, never a privileged bypass.
SELECT
    ds.id AS share_id, ds.permission, ds.status AS share_status,
    ds.expires_at, ds.created_at AS share_created_at, ds.created_by_user_id,
    d.id AS document_id, d.case_id, d.document_type, d.filename,
    d.description, d.mime_type, d.file_size, d.sha256_hash, d.status AS document_status,
    d.parent_document_id, d.uploaded_by, d.uploaded_at
FROM document_shares ds
JOIN documents d ON d.id = ds.document_id
WHERE ds.shared_with_user_id = $1
  AND ds.status = 'ACTIVE'
  AND (ds.expires_at IS NULL OR ds.expires_at > now())
ORDER BY ds.created_at DESC
LIMIT $2 OFFSET $3;

-- name: CountSharedWithMe :one
SELECT count(*)
FROM document_shares ds
WHERE ds.shared_with_user_id = $1
  AND ds.status = 'ACTIVE'
  AND (ds.expires_at IS NULL OR ds.expires_at > now());
