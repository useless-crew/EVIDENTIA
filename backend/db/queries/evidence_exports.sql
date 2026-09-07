-- name: CreateEvidenceExport :one
INSERT INTO evidence_exports (
    export_id,
    document_id,
    document_version,
    case_id,
    user_id,
    recipient_user_id,
    share_id,
    original_sha256,
    export_sha256,
    export_fingerprint,
    watermark_status,
    watermark_version,
    export_type,
    permission,
    source_ip,
    user_agent,
    session_reference_hash,
    status,
    metadata
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19
) RETURNING *;

-- name: GetEvidenceExportByID :one
SELECT * FROM evidence_exports
WHERE id = $1 LIMIT 1;

-- name: GetEvidenceExportByExportID :one
SELECT * FROM evidence_exports
WHERE export_id = $1 LIMIT 1;

-- name: UpdateEvidenceExportStatus :one
UPDATE evidence_exports
SET
    status = $2,
    failure_reason = $3,
    export_sha256 = COALESCE($4, export_sha256),
    completed_at = $5,
    audit_event_id = COALESCE($6, audit_event_id),
    blockchain_anchor_id = COALESCE($7, blockchain_anchor_id)
WHERE id = $1
RETURNING *;

-- name: ListEvidenceExportsByDocument :many
SELECT * FROM evidence_exports
WHERE document_id = $1
ORDER BY created_at DESC;

-- name: ListEvidenceExports :many
SELECT * FROM evidence_exports
ORDER BY created_at DESC
LIMIT $1 OFFSET $2;
