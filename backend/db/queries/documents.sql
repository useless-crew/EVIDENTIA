-- Evidentia — Document Queries
--
-- sha256_hash is BYTEA (raw 32 bytes) throughout — hex-encode only at the
-- API/JSON boundary (a later system's concern). Documents are never
-- deleted through these queries: there is no DeleteDocument query, and the
-- runtime role holds no DELETE grant on this table (see migration).

-- name: CreateDocument :one
INSERT INTO documents (
    case_id,
    parent_document_id,
    document_type,
    filename,
    description,
    mime_type,
    file_size,
    sha256_hash,
    storage_bucket,
    storage_object_key,
    metadata,
    uploaded_by
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12
)
RETURNING
    id, case_id, parent_document_id, document_type, filename, description,
    mime_type, file_size, sha256_hash, storage_bucket, storage_object_key,
    status, metadata, uploaded_by, uploaded_at, created_at, updated_at;

-- name: GetDocumentByID :one
SELECT
    id, case_id, parent_document_id, document_type, filename, description,
    mime_type, file_size, sha256_hash, storage_bucket, storage_object_key,
    status, metadata, uploaded_by, uploaded_at, created_at, updated_at
FROM documents
WHERE id = $1;

-- name: ListDocumentsByCase :many
SELECT
    id, case_id, parent_document_id, document_type, filename, description,
    mime_type, file_size, sha256_hash, storage_bucket, storage_object_key,
    status, metadata, uploaded_by, uploaded_at, created_at, updated_at
FROM documents
WHERE case_id = $1
ORDER BY uploaded_at DESC
LIMIT $2 OFFSET $3;

-- name: ListDocumentDerivatives :many
-- Derivative (e.g. redacted) documents produced from a given source.
SELECT
    id, case_id, parent_document_id, document_type, filename, description,
    mime_type, file_size, sha256_hash, storage_bucket, storage_object_key,
    status, metadata, uploaded_by, uploaded_at, created_at, updated_at
FROM documents
WHERE parent_document_id = $1
ORDER BY uploaded_at;

-- name: CountDocumentsByCase :one
SELECT count(*) FROM documents WHERE case_id = $1;

-- name: UpdateDocumentStatus :exec
UPDATE documents
SET status = $2, updated_at = now()
WHERE id = $1;
