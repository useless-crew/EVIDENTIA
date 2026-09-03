-- Evidentia — Certificate Integrity Binding (Down)

ALTER TABLE compliance_certificates
    DROP CONSTRAINT IF EXISTS compliance_certificates_document_hash_unique;
