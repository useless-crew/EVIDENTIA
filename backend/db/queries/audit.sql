-- Evidentia — Audit Log Queries
--
-- No update/delete query exists here, and none ever should: the runtime
-- role holds SELECT + INSERT only on audit_log (see migration and
-- backend/tests/audit_privileges_test.go). Hash-chain computation itself
-- (deriving hash from prev_hash + entry content) is System 8's job — these
-- queries only store/retrieve whatever the caller already computed.

-- name: InsertAuditEntry :one
INSERT INTO audit_log (
    user_id, role, action, resource_type, resource_id, case_id, metadata, prev_hash, hash
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9
)
RETURNING
    id, seq, "timestamp", user_id, role, action, resource_type, resource_id,
    case_id, metadata, prev_hash, hash;

-- name: GetLatestAuditEntry :one
-- The current chain head — System 8's writer reads this to learn the
-- prev_hash for the next entry it constructs.
SELECT
    id, seq, "timestamp", user_id, role, action, resource_type, resource_id,
    case_id, metadata, prev_hash, hash
FROM audit_log
ORDER BY seq DESC
LIMIT 1;

-- name: GetAuditEntryByID :one
SELECT
    id, seq, "timestamp", user_id, role, action, resource_type, resource_id,
    case_id, metadata, prev_hash, hash
FROM audit_log
WHERE id = $1;

-- name: ListAuditEntriesFromSeq :many
-- Chronological chain traversal starting just after fromSeq (pass 0 to
-- start from the genesis entry) — for chain verification (System 8).
SELECT
    id, seq, "timestamp", user_id, role, action, resource_type, resource_id,
    case_id, metadata, prev_hash, hash
FROM audit_log
WHERE seq > $1
ORDER BY seq
LIMIT $2;

-- name: ListAuditEntriesByCase :many
SELECT
    id, seq, "timestamp", user_id, role, action, resource_type, resource_id,
    case_id, metadata, prev_hash, hash
FROM audit_log
WHERE case_id = $1
ORDER BY seq DESC
LIMIT $2 OFFSET $3;

-- name: ListAuditEntriesByUser :many
SELECT
    id, seq, "timestamp", user_id, role, action, resource_type, resource_id,
    case_id, metadata, prev_hash, hash
FROM audit_log
WHERE user_id = $1
ORDER BY seq DESC
LIMIT $2 OFFSET $3;

-- name: ListAuditEntriesByAction :many
SELECT
    id, seq, "timestamp", user_id, role, action, resource_type, resource_id,
    case_id, metadata, prev_hash, hash
FROM audit_log
WHERE action = $1
ORDER BY seq DESC
LIMIT $2 OFFSET $3;

-- name: ListAuditEntriesByDateRange :many
SELECT
    id, seq, "timestamp", user_id, role, action, resource_type, resource_id,
    case_id, metadata, prev_hash, hash
FROM audit_log
WHERE "timestamp" >= $1 AND "timestamp" < $2
ORDER BY seq DESC
LIMIT $3 OFFSET $4;

-- name: CountAuditEntries :one
SELECT count(*) FROM audit_log;
