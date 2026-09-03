-- Evidentia — Certificate Integrity Binding (Up)
--
-- System 7 (Evidence Verification, Tamper Detection & Compliance
-- Certificates) needs to create a compliance certificate idempotently and
-- safely under concurrent requests: two simultaneous "generate a
-- certificate for this document" calls must never both succeed and
-- produce two rows. Application-level "check then insert" cannot
-- guarantee this by itself (master prompt §23) — a database-level
-- uniqueness constraint is required so the second, losing request can be
-- detected via a plain unique-violation error (or resolved atomically
-- with INSERT ... ON CONFLICT) rather than a race.
--
-- (document_id, document_hash), not document_id alone: this is a direct
-- expression of the table's own existing design intent (see its comment
-- in 000001_init_schema.up.sql — "a certificate is bound to the exact
-- document hash/version it represents"). In practice, since
-- documents.sha256_hash is immutable once set (System 6/7 both preserve
-- this — a verification mismatch never rewrites it), this constraint
-- limits a given document to at most one certificate today; it is
-- expressed against the (document_id, document_hash) PAIR rather than
-- document_id alone so it remains correct if a later system ever
-- legitimately produces more than one canonical hash per document_id
-- (e.g. a hypothetical future re-ingestion path) without requiring a
-- second migration to relax it.
ALTER TABLE compliance_certificates
    ADD CONSTRAINT compliance_certificates_document_hash_unique
    UNIQUE (document_id, document_hash);
