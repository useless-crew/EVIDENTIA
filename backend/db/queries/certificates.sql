-- Evidentia — Compliance Certificate Queries
--
-- Immutable once created: no update/delete query, and the runtime role
-- holds no UPDATE/DELETE grant on this table (see migration). document_hash
-- is stored redundantly alongside document_id so a certificate remains
-- historically meaningful even if the document's current metadata changes.

-- name: CreateCertificate :one
-- id and generated_at are passed explicitly (server-generated: id via
-- uuid.New(), generated_at via time.Now().UTC() in Go) rather than
-- relying on their DEFAULTs, because System 7's certificate signature is
-- computed over a canonical payload that includes both — the signed
-- value and the persisted value must be byte-for-byte identical, which a
-- database-side gen_random_uuid()/now() picked after signing could not
-- guarantee (see internal/service/certificate_service.go).
--
-- ON CONFLICT DO NOTHING on compliance_certificates_document_hash_unique
-- (000003_certificate_integrity.up.sql) makes concurrent "generate a
-- certificate for this document" requests safe: only one INSERT can ever
-- win for a given (document_id, document_hash) pair. A losing call
-- returns zero rows (pgx.ErrNoRows for this :one query) rather than an
-- error — the caller (CertificateService) treats that as "already
-- exists" and fetches the winning row via GetCertificateByDocumentAndHash,
-- never as a failure.
INSERT INTO compliance_certificates (
    id, document_id, document_hash, certificate_version, certificate_data, generated_by, generated_at
) VALUES (
    $1, $2, $3, $4, $5, $6, $7
)
ON CONFLICT ON CONSTRAINT compliance_certificates_document_hash_unique DO NOTHING
RETURNING id, document_id, document_hash, certificate_version, certificate_data, generated_by, generated_at, created_at;

-- name: GetCertificateByID :one
SELECT id, document_id, document_hash, certificate_version, certificate_data, generated_by, generated_at, created_at
FROM compliance_certificates
WHERE id = $1;

-- name: GetCertificateByDocumentID :one
-- At most one row can ever match in practice (see the unique constraint's
-- own comment: documents.sha256_hash is immutable, so a document has at
-- most one distinct hash to be paired with) — LIMIT 1 makes that
-- assumption explicit rather than relying on it silently.
SELECT id, document_id, document_hash, certificate_version, certificate_data, generated_by, generated_at, created_at
FROM compliance_certificates
WHERE document_id = $1
ORDER BY generated_at DESC
LIMIT 1;

-- name: GetCertificateByDocumentAndHash :one
-- Used to fetch the existing certificate after a CreateCertificate
-- ON CONFLICT DO NOTHING resolves to "already exists" — see CreateCertificate.
SELECT id, document_id, document_hash, certificate_version, certificate_data, generated_by, generated_at, created_at
FROM compliance_certificates
WHERE document_id = $1 AND document_hash = $2;

-- name: ListCertificatesByDocument :many
SELECT id, document_id, document_hash, certificate_version, certificate_data, generated_by, generated_at, created_at
FROM compliance_certificates
WHERE document_id = $1
ORDER BY generated_at DESC;
