-- Evidentia — Redaction Queries
--
-- Redactions are immutable once created: no update/delete query, and the
-- runtime role holds no UPDATE/DELETE grant on this table (see migration).

-- name: CreateRedaction :one
INSERT INTO redactions (source_document_id, result_document_id, region_data, reason, created_by)
VALUES ($1, $2, $3, $4, $5)
RETURNING id, source_document_id, result_document_id, region_data, reason, created_by, created_at;

-- name: GetRedactionByResultDocument :one
SELECT id, source_document_id, result_document_id, region_data, reason, created_by, created_at
FROM redactions
WHERE result_document_id = $1;

-- name: ListRedactionsBySourceDocument :many
SELECT id, source_document_id, result_document_id, region_data, reason, created_by, created_at
FROM redactions
WHERE source_document_id = $1
ORDER BY created_at;
