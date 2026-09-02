-- Evidentia — Compliance Certificate Queries
--
-- Immutable once created: no update/delete query, and the runtime role
-- holds no UPDATE/DELETE grant on this table (see migration). document_hash
-- is stored redundantly alongside document_id so a certificate remains
-- historically meaningful even if the document's current metadata changes.

-- name: CreateCertificate :one
INSERT INTO compliance_certificates (
    document_id, document_hash, certificate_version, certificate_data, generated_by
) VALUES (
    $1, $2, $3, $4, $5
)
RETURNING id, document_id, document_hash, certificate_version, certificate_data, generated_by, generated_at, created_at;

-- name: GetCertificateByID :one
SELECT id, document_id, document_hash, certificate_version, certificate_data, generated_by, generated_at, created_at
FROM compliance_certificates
WHERE id = $1;

-- name: ListCertificatesByDocument :many
SELECT id, document_id, document_hash, certificate_version, certificate_data, generated_by, generated_at, created_at
FROM compliance_certificates
WHERE document_id = $1
ORDER BY generated_at DESC;
