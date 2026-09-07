-- Evidentia — Secure Evidence Export (Up)
--
-- This migration establishes the evidence_exports table for tracking
-- authorized document exports, tying them to a cryptographically secure
-- export fingerprint, watermark metadata, and auditing context.

CREATE TABLE evidence_exports (
    id                     UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    export_id              TEXT NOT NULL,
    document_id            UUID NOT NULL REFERENCES documents(id) ON DELETE RESTRICT,
    document_version       INT NOT NULL DEFAULT 1,
    case_id                UUID NOT NULL REFERENCES cases(id) ON DELETE RESTRICT,
    user_id                UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    recipient_user_id      UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    share_id               UUID REFERENCES document_shares(id) ON DELETE RESTRICT,
    
    original_sha256        BYTEA NOT NULL,
    export_sha256          BYTEA,
    export_fingerprint     BYTEA NOT NULL,
    
    watermark_status       TEXT NOT NULL,
    watermark_version      INT NOT NULL DEFAULT 1,
    
    export_type            TEXT NOT NULL,
    permission             TEXT NOT NULL,
    
    source_ip              TEXT,
    user_agent             TEXT,
    session_reference_hash TEXT,
    
    status                 TEXT NOT NULL DEFAULT 'REQUESTED',
    failure_reason         TEXT,
    
    audit_event_id         UUID REFERENCES audit_log(id) ON DELETE RESTRICT,
    blockchain_anchor_id   UUID REFERENCES blockchain_anchors(id) ON DELETE RESTRICT,
    metadata               JSONB NOT NULL DEFAULT '{}'::jsonb,
    
    created_at             TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at           TIMESTAMPTZ,

    CONSTRAINT evidence_exports_export_id_unique UNIQUE (export_id),
    CONSTRAINT evidence_exports_status_check CHECK (status IN ('REQUESTED', 'PROCESSING', 'COMPLETED', 'FAILED', 'REVOKED')),
    CONSTRAINT evidence_exports_watermark_status_check CHECK (watermark_status IN ('APPLIED', 'NOT_SUPPORTED', 'FAILED', 'PENDING')),
    CONSTRAINT evidence_exports_original_sha256_length CHECK (octet_length(original_sha256) = 32),
    CONSTRAINT evidence_exports_export_sha256_length CHECK (export_sha256 IS NULL OR octet_length(export_sha256) = 32),
    CONSTRAINT evidence_exports_fingerprint_length CHECK (octet_length(export_fingerprint) = 32)
);

COMMENT ON TABLE evidence_exports IS
    'Tracks every authorized export/download of a document. Contains cryptographic fingerprints, '
    'provenance hashes, watermark statuses, and audit references.';

CREATE INDEX idx_evidence_exports_export_id ON evidence_exports(export_id);
CREATE INDEX idx_evidence_exports_document_id ON evidence_exports(document_id);
CREATE INDEX idx_evidence_exports_case_id ON evidence_exports(case_id);
CREATE INDEX idx_evidence_exports_user_id ON evidence_exports(user_id);
CREATE INDEX idx_evidence_exports_recipient_user_id ON evidence_exports(recipient_user_id);
CREATE INDEX idx_evidence_exports_share_id ON evidence_exports(share_id);
CREATE INDEX idx_evidence_exports_status ON evidence_exports(status);

ALTER TABLE evidence_exports ENABLE ROW LEVEL SECURITY;
ALTER TABLE evidence_exports FORCE ROW LEVEL SECURITY;

-- Select policy: ADMIN can see all. Users can see exports they requested or received.
CREATE POLICY evidence_exports_select ON evidence_exports FOR SELECT
    USING (
        current_app_role() = 'ADMIN'
        OR user_id = current_app_user_id()
        OR recipient_user_id = current_app_user_id()
    );

-- Insert policy
CREATE POLICY evidence_exports_insert ON evidence_exports FOR INSERT
    WITH CHECK (
        current_app_user_id() IS NOT NULL
        AND user_id = current_app_user_id()
    );

-- Update policy (for status updates by the worker or the original user)
CREATE POLICY evidence_exports_update ON evidence_exports FOR UPDATE
    USING (
        current_app_role() = 'ADMIN'
        OR user_id = current_app_user_id()
    );

GRANT SELECT, INSERT, UPDATE ON evidence_exports TO evidentia_app;
REVOKE DELETE ON evidence_exports FROM evidentia_app;
