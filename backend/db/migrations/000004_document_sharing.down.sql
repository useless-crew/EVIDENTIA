-- Evidentia — Secure Document Sharing & Access Delegation (Down)

ALTER POLICY compliance_certificates_select ON compliance_certificates USING (
    current_app_role() = 'ADMIN'
    OR EXISTS (
        SELECT 1 FROM documents d
        JOIN case_members cm ON cm.case_id = d.case_id
        WHERE d.id = compliance_certificates.document_id
          AND cm.user_id = current_app_user_id()
          AND cm.removed_at IS NULL
    )
);

ALTER POLICY documents_select ON documents USING (
    current_app_role() = 'ADMIN'
    OR EXISTS (
        SELECT 1 FROM case_members cm
        WHERE cm.case_id = documents.case_id
          AND cm.user_id = current_app_user_id()
          AND cm.removed_at IS NULL
    )
);

DROP FUNCTION IF EXISTS has_active_document_share(UUID, UUID);

DROP TABLE IF EXISTS document_shares;
