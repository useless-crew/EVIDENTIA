-- Evidentia — Audit Chain Verification Job Queries (System 11)
--
-- These queries only persist/retrieve the LIFECYCLE of a verification run
-- (audit_verifications). The actual cryptographic check — reading
-- audit_log in chain order and recomputing hashes — reuses System 10's
-- existing audit.sql queries (InsertAuditEntry's neighbors:
-- GetLatestAuditEntry, ListAuditEntriesFromSeq, CountAuditEntries)
-- unchanged; nothing here duplicates that.

-- name: CreateAuditVerification :one
-- Always attempted first by AuditService.StartVerification; a unique-
-- violation on idx_audit_verifications_single_active (at most one
-- QUEUED/RUNNING row at a time) means a verification is already active —
-- the caller catches that specific conflict and calls
-- GetActiveAuditVerification instead of treating it as an error.
INSERT INTO audit_verifications (
    requested_by_user_id, requested_by_role
) VALUES (
    $1, $2
)
RETURNING
    id, requested_by_user_id, requested_by_role, status, entries_checked,
    total_entries, last_seq_checked, failed_entry_id, failed_seq,
    failure_type, failure_reason, started_at, completed_at, created_at, updated_at;

-- name: GetActiveAuditVerification :one
-- At most one row can ever match (idx_audit_verifications_single_active),
-- so LIMIT 1 is defensive rather than load-bearing.
SELECT
    id, requested_by_user_id, requested_by_role, status, entries_checked,
    total_entries, last_seq_checked, failed_entry_id, failed_seq,
    failure_type, failure_reason, started_at, completed_at, created_at, updated_at
FROM audit_verifications
WHERE status IN ('QUEUED', 'RUNNING')
ORDER BY created_at DESC
LIMIT 1;

-- name: GetAuditVerificationByID :one
SELECT
    id, requested_by_user_id, requested_by_role, status, entries_checked,
    total_entries, last_seq_checked, failed_entry_id, failed_seq,
    failure_type, failure_reason, started_at, completed_at, created_at, updated_at
FROM audit_verifications
WHERE id = $1;

-- name: MarkAuditVerificationRunning :one
-- The `AND status = 'QUEUED'` guard makes this safe against Asynq ever
-- redelivering/retrying the same task concurrently: a second attempt to
-- start an already-RUNNING job matches zero rows (sqlc surfaces this as
-- pgx.ErrNoRows for :one), which the handler treats as "someone else
-- already started it" rather than corrupting total_entries/started_at.
UPDATE audit_verifications
SET status = 'RUNNING', started_at = now(), total_entries = $2, updated_at = now()
WHERE id = $1 AND status = 'QUEUED'
RETURNING
    id, requested_by_user_id, requested_by_role, status, entries_checked,
    total_entries, last_seq_checked, failed_entry_id, failed_seq,
    failure_type, failure_reason, started_at, completed_at, created_at, updated_at;

-- name: UpdateAuditVerificationProgress :exec
-- Called at a throttled cadence (see internal/service.AuditService's
-- progress-update interval), never once per audit_log row — one UPDATE
-- per verification BATCH (see internal/audit.VerifyBatch), which is
-- already a small, bounded number of writes even for a very large chain.
UPDATE audit_verifications
SET entries_checked = $2, last_seq_checked = $3, updated_at = now()
WHERE id = $1 AND status = 'RUNNING';

-- name: CompleteAuditVerification :one
-- Sets the terminal state exactly once. failed_entry_id/failed_seq/
-- failure_type/failure_reason are NULL for VERIFIED (see the table's own
-- audit_verifications_failure_fields_check constraint, which rejects any
-- other combination).
UPDATE audit_verifications
SET status = $2,
    entries_checked = $3,
    failed_entry_id = sqlc.narg(failed_entry_id),
    failed_seq = sqlc.narg(failed_seq),
    failure_type = sqlc.narg(failure_type),
    failure_reason = sqlc.narg(failure_reason),
    completed_at = now(),
    updated_at = now()
WHERE id = $1
RETURNING
    id, requested_by_user_id, requested_by_role, status, entries_checked,
    total_entries, last_seq_checked, failed_entry_id, failed_seq,
    failure_type, failure_reason, started_at, completed_at, created_at, updated_at;

-- name: MarkAuditVerificationStale :one
-- The read-time self-healing path (AuditService.reconcileStale): a
-- QUEUED/RUNNING row whose updated_at is older than the staleness
-- threshold is presumed to belong to a worker that crashed/was killed
-- without ever reaching CompleteAuditVerification. The `AND status IN
-- (...)` guard makes this safe to call speculatively on every read of a
-- non-terminal row — if the real worker completes it in the same instant,
-- this matches zero rows (pgx.ErrNoRows) and the caller just re-fetches
-- the now-terminal row instead.
UPDATE audit_verifications
SET status = 'FAILED',
    failure_type = 'STALE_TIMEOUT',
    failure_reason = sqlc.arg(failure_reason),
    completed_at = now(),
    updated_at = now()
WHERE id = $1 AND status IN ('QUEUED', 'RUNNING')
RETURNING
    id, requested_by_user_id, requested_by_role, status, entries_checked,
    total_entries, last_seq_checked, failed_entry_id, failed_seq,
    failure_type, failure_reason, started_at, completed_at, created_at, updated_at;

-- name: ListAuditVerificationsFiltered :many
-- GET /audit/verifications: every filter optional (NULL = "no
-- constraint"), same convention as ListAuditEntriesFiltered.
SELECT
    id, requested_by_user_id, requested_by_role, status, entries_checked,
    total_entries, last_seq_checked, failed_entry_id, failed_seq,
    failure_type, failure_reason, started_at, completed_at, created_at, updated_at
FROM audit_verifications
WHERE (sqlc.narg(status)::text IS NULL OR status = sqlc.narg(status))
  AND (sqlc.narg(requested_by_user_id)::uuid IS NULL OR requested_by_user_id = sqlc.narg(requested_by_user_id))
  AND (sqlc.narg(from_ts)::timestamptz IS NULL OR created_at >= sqlc.narg(from_ts))
  AND (sqlc.narg(to_ts)::timestamptz IS NULL OR created_at < sqlc.narg(to_ts))
ORDER BY created_at DESC
LIMIT sqlc.arg(limit_val) OFFSET sqlc.arg(offset_val);

-- name: CountAuditVerificationsFiltered :one
SELECT count(*) FROM audit_verifications
WHERE (sqlc.narg(status)::text IS NULL OR status = sqlc.narg(status))
  AND (sqlc.narg(requested_by_user_id)::uuid IS NULL OR requested_by_user_id = sqlc.narg(requested_by_user_id))
  AND (sqlc.narg(from_ts)::timestamptz IS NULL OR created_at >= sqlc.narg(from_ts))
  AND (sqlc.narg(to_ts)::timestamptz IS NULL OR created_at < sqlc.narg(to_ts));

-- name: GetLatestAuditVerification :one
-- The dashboard summary's "last verification status/timestamp" — the
-- most recently CREATED run regardless of status (a caller wanting only
-- completed runs filters client-side on the small result, or calls
-- ListAuditVerificationsFiltered with status set).
SELECT
    id, requested_by_user_id, requested_by_role, status, entries_checked,
    total_entries, last_seq_checked, failed_entry_id, failed_seq,
    failure_type, failure_reason, started_at, completed_at, created_at, updated_at
FROM audit_verifications
ORDER BY created_at DESC
LIMIT 1;
