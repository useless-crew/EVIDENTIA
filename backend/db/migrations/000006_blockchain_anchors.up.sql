-- Evidentia — Hyperledger Fabric Blockchain Anchors (Up)
--
-- System 20 adds Hyperledger Fabric as a permissioned blockchain-based
-- evidence provenance and integrity layer. This table records every
-- blockchain anchor attempt: a cryptographic hash + provenance reference
-- committed to the Fabric ledger to prove that a given document existed
-- in a known state at a known time, endorsed by the configured
-- organizations.
--
-- Architecture:
--   Documents are stored in MinIO (off-chain), SHA-256 hashes in
--   PostgreSQL (documents.sha256_hash), and NOW also anchored to the
--   Hyperledger Fabric ledger for tamper-evident provenance. This table
--   is the outbox/status-tracking layer between PostgreSQL and Fabric —
--   it does NOT store evidence content, and is NOT a replacement for
--   the existing audit_log chain.
--
-- Consistency model:
--   1. Document upload → PostgreSQL transaction commits (document record).
--   2. Blockchain anchor record inserted here with status PENDING.
--   3. Asynq worker picks up the task (TypeBlockchainAnchor).
--   4. Worker submits to Fabric; updates status to CONFIRMED or FAILED.
--
--   If the worker crashes between steps 3 and 4, the PENDING/FAILED row
--   enables recovery: the operator or a scheduled reconciliation job can
--   re-enqueue any PENDING row that has not progressed beyond its
--   deadline.
--
-- This migration does NOT modify any existing table or index.

-- =============================================================================
-- 1. BLOCKCHAIN_ANCHORS
-- =============================================================================

CREATE TABLE blockchain_anchors (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    document_id     UUID NOT NULL REFERENCES documents(id) ON DELETE RESTRICT,
    -- Version mirrors documents.version (or a per-document counter).
    -- For System 20, we track which logical "version" of the document
    -- (after redaction, etc.) this anchor corresponds to. Version 1 is
    -- always the original upload anchor.
    document_version INT NOT NULL DEFAULT 1,
    -- The event that caused this anchor to be created.
    -- Values: DOCUMENT_UPLOADED, DOCUMENT_REDACTED, CERTIFICATE_ISSUED
    event_type      TEXT NOT NULL,
    -- SHA-256 of the document at the time this anchor was requested.
    -- Stored redundantly here (rather than only relying on
    -- documents.sha256_hash) so the anchor record is self-contained:
    -- if the document record is later amended or the document is
    -- superseded, the anchor still carries the hash it committed to
    -- the ledger. Exactly 32 bytes (the output of SHA-256), matching
    -- the convention documents.sha256_hash already uses.
    document_hash   BYTEA NOT NULL,
    -- Lifecycle: PENDING → CONFIRMED (happy path)
    --            PENDING → FAILED (after all retries exhausted)
    -- PENDING: anchor row created; worker not yet started.
    -- CONFIRMED: Fabric transaction committed and validated.
    -- FAILED: terminal failure; see last_error/retry_count.
    status          TEXT NOT NULL DEFAULT 'PENDING',
    -- Fabric transaction ID returned on successful commit. NULL until
    -- status transitions to CONFIRMED. Never a placeholder or a
    -- randomly generated local value — the REAL Fabric tx_id returned
    -- by fabric-gateway after the transaction is committed.
    fabric_tx_id    TEXT,
    -- The Fabric channel this anchor was submitted to.
    fabric_channel  TEXT,
    -- The chaincode name that processed this anchor.
    fabric_chaincode TEXT,
    -- The MSP organization identity that submitted the transaction.
    -- For System 20, this is always the backend's configured org
    -- (e.g. "PoliceMSP"). Captured here so provenance is traceable
    -- even if the configuration later changes.
    organization    TEXT,
    -- Human-readable error message if status = FAILED. Never a raw
    -- stack trace, SQL error, or internal credential — see the
    -- master prompt's "do not leak internal details" rule.
    last_error      TEXT,
    -- How many times the worker retried this anchor before giving up.
    retry_count     INT NOT NULL DEFAULT 0,
    -- Optional arbitrary JSON for future extensibility (e.g. endorser
    -- list, policy used, additional reference IDs). Must never contain
    -- document content, private keys, or sensitive personal data.
    metadata        JSONB,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- NULL until status = CONFIRMED.
    confirmed_at    TIMESTAMPTZ,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT blockchain_anchors_status_check CHECK (
        status IN ('PENDING', 'CONFIRMED', 'FAILED')
    ),
    -- document_hash must be exactly 32 bytes (SHA-256 output).
    CONSTRAINT blockchain_anchors_hash_length CHECK (
        octet_length(document_hash) = 32
    ),
    -- event_type is a closed vocabulary in System 20.
    CONSTRAINT blockchain_anchors_event_type_check CHECK (
        event_type IN ('DOCUMENT_UPLOADED', 'DOCUMENT_REDACTED', 'CERTIFICATE_ISSUED')
    ),
    -- confirmed_at is only ever set for CONFIRMED rows.
    CONSTRAINT blockchain_anchors_confirmed_at_check CHECK (
        (status = 'CONFIRMED' AND confirmed_at IS NOT NULL)
        OR (status != 'CONFIRMED' AND confirmed_at IS NULL)
    ),
    -- fabric_tx_id is only ever set for CONFIRMED rows.
    CONSTRAINT blockchain_anchors_tx_id_check CHECK (
        (status = 'CONFIRMED' AND fabric_tx_id IS NOT NULL)
        OR (status != 'CONFIRMED' AND fabric_tx_id IS NULL)
    ),
    -- version must be at least 1.
    CONSTRAINT blockchain_anchors_version_positive CHECK (document_version >= 1),
    -- retry_count must not be negative.
    CONSTRAINT blockchain_anchors_retry_count_check CHECK (retry_count >= 0)
);

COMMENT ON TABLE blockchain_anchors IS
    'One row per Hyperledger Fabric blockchain anchoring attempt for an '
    'Evidentia document (System 20). Stores the document SHA-256, event type, '
    'Fabric transaction ID (once confirmed), and lifecycle status. Evidence '
    'content is NEVER stored here — only cryptographic references. '
    'This table is the outbox between PostgreSQL and the Fabric network; '
    'it does NOT replace the existing audit_log chain.';

COMMENT ON COLUMN blockchain_anchors.document_hash IS
    'SHA-256 of the document at anchor time — exactly 32 bytes, matching '
    'documents.sha256_hash. Stored redundantly here so the anchor record '
    'is self-contained and survives document record evolution.';

COMMENT ON COLUMN blockchain_anchors.fabric_tx_id IS
    'The real Hyperledger Fabric transaction ID returned by the peer after '
    'the transaction is committed to the ledger. NULL until CONFIRMED. '
    'Never a fabricated/local placeholder.';

COMMENT ON COLUMN blockchain_anchors.status IS
    'PENDING: anchor requested, not yet submitted. '
    'CONFIRMED: Fabric tx committed and validated. '
    'FAILED: terminal failure after retries exhausted.';

-- =============================================================================
-- 2. INDEXES
-- =============================================================================

-- The primary lookup: "what is the blockchain status of this document?"
CREATE INDEX idx_blockchain_anchors_document_id
    ON blockchain_anchors(document_id);

-- Status-based queries: "how many PENDING anchors are waiting?"
-- Used by the reconciliation / monitoring paths.
CREATE INDEX idx_blockchain_anchors_status
    ON blockchain_anchors(status);

-- Time-ordered listing (provenance history queries).
CREATE INDEX idx_blockchain_anchors_created_at
    ON blockchain_anchors(created_at DESC);

-- Fabric tx_id lookup: "does this tx_id correspond to a known anchor?"
-- Partial index — only CONFIRMED rows have a non-NULL tx_id.
CREATE INDEX idx_blockchain_anchors_fabric_tx_id
    ON blockchain_anchors(fabric_tx_id)
    WHERE fabric_tx_id IS NOT NULL;

-- Composite: per-document, time-ordered, status-filtered queries.
CREATE INDEX idx_blockchain_anchors_doc_status_created
    ON blockchain_anchors(document_id, status, created_at DESC);

-- =============================================================================
-- 3. ROW-LEVEL SECURITY
-- =============================================================================
--
-- Blockchain anchor records are evidence metadata — they follow the same
-- access model as documents themselves: a user can see blockchain anchors
-- for documents they are authorized to access.
--
-- Because blockchain_anchors references documents, and documents already
-- have RLS enforcing case-membership, we can implement the policy by
-- checking whether the actor can access the related document.
--
-- ADMIN: sees all anchors (consistent with seeing all documents).
-- Others: see anchors for documents in their accessible cases only.
--
-- This mirrors how compliance_certificates handles the same pattern.
ALTER TABLE blockchain_anchors ENABLE ROW LEVEL SECURITY;
ALTER TABLE blockchain_anchors FORCE ROW LEVEL SECURITY;

CREATE POLICY blockchain_anchors_select ON blockchain_anchors FOR SELECT
    USING (
        current_app_role() = 'ADMIN'
        OR EXISTS (
            SELECT 1 FROM documents d
            JOIN cases c ON c.id = d.case_id
            JOIN case_members cm ON cm.case_id = c.id
            WHERE d.id = blockchain_anchors.document_id
              AND cm.user_id = current_app_user_id()
              AND cm.removed_at IS NULL
        )
    );

-- INSERT: the backend service account (runs as ADMIN-equivalent in
-- transactions that write anchor records). This mirrors how
-- audit_verifications handles INSERT with the same ADMIN pattern.
CREATE POLICY blockchain_anchors_insert ON blockchain_anchors FOR INSERT
    WITH CHECK (current_app_role() = 'ADMIN' OR current_app_user_id() IS NOT NULL);

-- UPDATE: the Asynq worker updating status/tx_id/confirmed_at/etc.
-- Runs under the same ADMIN-equivalent identity, consistent with
-- audit_verifications_update.
CREATE POLICY blockchain_anchors_update ON blockchain_anchors FOR UPDATE
    USING (current_app_role() = 'ADMIN');

-- No DELETE: blockchain anchor records are permanent evidence metadata.
-- Consistent with audit_log, compliance_certificates, redactions.

GRANT SELECT, INSERT, UPDATE ON blockchain_anchors TO evidentia_app;
REVOKE DELETE ON blockchain_anchors FROM evidentia_app;
