-- Evidentia — Audit Chain Verification & Integrity Dashboard (Up)
--
-- System 10 (000001's audit_log + internal/audit) established the
-- cryptographically hash-chained, append-only audit trail and a
-- synchronous, single-HTTP-call verification path. System 11 adds
-- ASYNCHRONOUS verification (Asynq-dispatched, so a chain of any size
-- never has to be checked within one HTTP request's lifetime) and, per
-- master prompt's "PostgreSQL should contain the authoritative
-- verification record" (not Redis alone), a durable, queryable history
-- of every verification ever run: this table.
--
-- This does NOT duplicate or reimplement anything System 10 already
-- owns: no new hash/canonicalization/chain-traversal logic lives in this
-- schema, and audit_log itself is completely untouched by this
-- migration. A row here only ever RECORDS the outcome of running System
-- 10's existing verifier (internal/audit.VerifyBatch/ComputeEntryHash)
-- against audit_log — it never feeds back into or alters the chain.

-- =============================================================================
-- 1. AUDIT_VERIFICATIONS
-- =============================================================================

CREATE TABLE audit_verifications (
    id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    requested_by_user_id  UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    requested_by_role     TEXT,
    status                TEXT NOT NULL DEFAULT 'QUEUED',
    entries_checked       BIGINT NOT NULL DEFAULT 0,
    total_entries         BIGINT,
    last_seq_checked      BIGINT,
    failed_entry_id       UUID,
    failed_seq            BIGINT,
    failure_type          TEXT,
    failure_reason        TEXT,
    started_at            TIMESTAMPTZ,
    completed_at          TIMESTAMPTZ,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT audit_verifications_status_check CHECK (
        status IN ('QUEUED', 'RUNNING', 'VERIFIED', 'INTEGRITY_FAILURE', 'FAILED')
    ),
    -- started_at/completed_at are only ever set once their corresponding
    -- transition has actually happened — never backfilled/guessed. A
    -- QUEUED row has neither; RUNNING has started_at only; VERIFIED/
    -- INTEGRITY_FAILURE (only reachable by having actually run) always
    -- have both. FAILED is the one exception allowed to have a NULL
    -- started_at: a job can fail operationally before a worker ever
    -- picked it up (e.g. Asynq's own retry budget exhausted while
    -- PostgreSQL/Redis was unreachable) — see internal/service.
    -- AuditService.MarkVerificationOperationallyFailed.
    CONSTRAINT audit_verifications_lifecycle_timestamps_check CHECK (
        (status = 'QUEUED' AND started_at IS NULL AND completed_at IS NULL)
        OR (status = 'RUNNING' AND started_at IS NOT NULL AND completed_at IS NULL)
        OR (status IN ('VERIFIED', 'INTEGRITY_FAILURE') AND started_at IS NOT NULL AND completed_at IS NOT NULL)
        OR (status = 'FAILED' AND completed_at IS NOT NULL)
    ),
    -- failed_entry_id/failed_seq/failure_type/failure_reason are only
    -- ever populated for INTEGRITY_FAILURE or FAILED — never for QUEUED/
    -- RUNNING/VERIFIED, so a caller can trust their mere presence as "this
    -- run found a problem" without also checking status.
    CONSTRAINT audit_verifications_failure_fields_check CHECK (
        (status IN ('INTEGRITY_FAILURE', 'FAILED') AND failure_type IS NOT NULL)
        OR (status NOT IN ('INTEGRITY_FAILURE', 'FAILED')
            AND failed_entry_id IS NULL AND failed_seq IS NULL
            AND failure_type IS NULL AND failure_reason IS NULL)
    )
);

COMMENT ON TABLE audit_verifications IS
    'One row per audit-chain verification run (System 11) — the durable, '
    'evidentiary record of "was the chain intact as of this check", '
    'independent of whatever transport (SSE, polling) a client used to '
    'observe it while running. Never mutates audit_log; read-only against '
    'it. id is the verification_id every System 11 API/SSE route is keyed '
    'by. requested_by_role is captured verbatim at request time, mirroring '
    'audit_log.role''s own rationale (a user''s roles can change after the '
    'fact, but this record should reflect what was true when requested).';
COMMENT ON COLUMN audit_verifications.total_entries IS
    'Captured ONCE, at job start, from a single COUNT(*) — never '
    're-queried per batch (master prompt: "do not perform an expensive '
    'COUNT query repeatedly"). NULL only in the brief QUEUED window before '
    'the worker has picked the job up.';
COMMENT ON COLUMN audit_verifications.last_seq_checked IS
    'audit_log.seq of the last entry confirmed valid so far — the live '
    'progress cursor, updated at the same throttled cadence as '
    'entries_checked (see internal/service.AuditService''s progress-'
    'update-throttle constant). Never used to RESUME a crashed run (see '
    'failure_type''s STALE_TIMEOUT case below) — only to report progress.';
COMMENT ON COLUMN audit_verifications.failure_type IS
    'For INTEGRITY_FAILURE: one of GENESIS_INVALID, '
    'PREVIOUS_HASH_MISMATCH, ENTRY_HASH_MISMATCH, CANONICALIZATION_ERROR '
    '(see internal/audit.BatchResult.FailureType — the exact, and only, '
    'categories System 10''s verifier can DEFINITIVELY distinguish; '
    'CHAIN_FORK_DETECTED/DUPLICATE_ENTRY/CHAIN_ORDER_INVALID are '
    'prevented at the database level by audit_log''s own unique indexes/ '
    'identity column and therefore can never be witnessed by a verifier '
    'scanning a successfully-committed chain — see docs/AUDIT_CHAIN.md). '
    'For FAILED: an operational category such as DATABASE_ERROR, TIMEOUT, '
    'or STALE_TIMEOUT (a RUNNING/QUEUED row whose worker went silent long '
    'enough to be presumed dead — see AuditService''s reconciliation '
    'logic) — never confused with a cryptographic finding.';
COMMENT ON COLUMN audit_verifications.failure_reason IS
    'Safe, human-readable detail only — never a raw SQL error, stack '
    'trace, filesystem path, or credential (master prompt: "do not '
    'expose... database credentials... SQL statements... internal '
    'filesystem paths").';

CREATE INDEX idx_audit_verifications_status ON audit_verifications(status);
CREATE INDEX idx_audit_verifications_created_at ON audit_verifications(created_at DESC);
CREATE INDEX idx_audit_verifications_requested_by ON audit_verifications(requested_by_user_id);

-- At most one QUEUED-or-RUNNING verification at a time — the exact same
-- "unique index on a constant expression, filtered to the rows in
-- question" idiom audit_log's own idx_audit_log_single_genesis already
-- established (000001's migration), reused here for the identical
-- reason: a hard, database-level guarantee (not merely an
-- application-level check-then-insert race) that two concurrent
-- POST /audit/verify-chain calls can never both create a second
-- simultaneously-running full-chain scan. AuditService.StartVerification
-- treats a 23505 conflict on this index as "an active job already
-- exists" and returns that existing row instead of erroring.
CREATE UNIQUE INDEX idx_audit_verifications_single_active
    ON audit_verifications((1)) WHERE status IN ('QUEUED', 'RUNNING');

-- =============================================================================
-- 2. ROW-LEVEL SECURITY — audit_verifications
-- =============================================================================
--
-- Verifying/inspecting the GLOBAL audit chain is ADMIN-only, exactly like
-- POST /audit/verify-chain already was in System 10 (audit:verify is
-- granted only to ADMIN — see db/seed/001_reference_data.sql) and for
-- the identical reason documented there: checking "the chain" only means
-- anything against the complete, unfiltered sequence, so there is no
-- narrower, non-ADMIN-scoped view of a verification run to expose.
ALTER TABLE audit_verifications ENABLE ROW LEVEL SECURITY;
ALTER TABLE audit_verifications FORCE ROW LEVEL SECURITY;

CREATE POLICY audit_verifications_select ON audit_verifications FOR SELECT
    USING (current_app_role() = 'ADMIN');

CREATE POLICY audit_verifications_insert ON audit_verifications FOR INSERT
    WITH CHECK (current_app_role() = 'ADMIN' AND current_app_user_id() IS NOT NULL);

-- UPDATE is the worker's own progress/completion writes (entries_checked,
-- status, last_seq_checked, failure_*, started_at/completed_at,
-- updated_at) — never a field an API client can set directly; no handler
-- accepts any of these as request input (see
-- internal/handlers/audit's dto.go). Same ADMIN-only condition as
-- SELECT/INSERT: the worker always runs under an ADMIN-equivalent
-- transaction identity for this table, mirroring internal/audit.
-- ChainWriter's own established chainWriterRLSRole pattern for exactly
-- the same reason (ordinary RLS would otherwise scope updates to "rows
-- this actor's own identity owns", which is not the right model for a
-- background worker that must be able to progress ANY verification row
-- regardless of which admin originally requested it).
CREATE POLICY audit_verifications_update ON audit_verifications FOR UPDATE
    USING (current_app_role() = 'ADMIN');

-- No DELETE policy or grant: a verification run is a permanent
-- evidentiary record once created, exactly like redactions/
-- compliance_certificates/document_shares.

GRANT SELECT, INSERT, UPDATE ON audit_verifications TO evidentia_app;
REVOKE DELETE ON audit_verifications FROM evidentia_app;
