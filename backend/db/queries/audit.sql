-- Evidentia — Audit Log Queries
--
-- No update/delete query exists here, and none ever should: the runtime
-- role holds SELECT + INSERT only on audit_log (see migration and
-- backend/tests/audit_privileges_test.go). Hash-chain computation itself
-- (canonicalizing an entry, deriving hash from prev_hash + content) is
-- internal/audit's job (see chain.go/writer.go) — these queries only
-- store/retrieve whatever the caller already computed.

-- name: InsertAuditEntry :one
-- id and "timestamp" are supplied explicitly by the caller (internal/
-- audit.ChainWriter), NOT left to their column DEFAULTs
-- (gen_random_uuid()/now()) — the writer must know both values BEFORE
-- this INSERT runs, since they are themselves inputs to the entry's hash
-- (you cannot hash a value you haven't decided yet). This mirrors
-- exactly how CertificateService already generates certID/issuedAt in Go
-- before signing, for the identical reason. seq is the one field that
-- genuinely cannot be supplied this way (GENERATED ALWAYS AS IDENTITY
-- rejects an explicit value) — it is deliberately excluded from the hash
-- input for that reason; see chain.go's doc comment.
INSERT INTO audit_log (
    id, "timestamp", user_id, role, action, resource_type, resource_id, case_id, metadata, prev_hash, hash
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11
)
RETURNING
    id, seq, "timestamp", user_id, role, action, resource_type, resource_id,
    case_id, metadata, prev_hash, hash;

-- name: AcquireAuditChainLock :exec
-- A PostgreSQL transaction-scoped advisory lock (automatically released
-- at COMMIT/ROLLBACK, never leaked across a pooled connection) —
-- internal/audit.ChainWriter takes this BEFORE reading the current chain
-- head, so at most one transaction at a time can be "between" reading
-- the head and inserting the entry that claims it as its predecessor.
-- This is the authoritative, database-level guarantee that concurrent
-- writers cannot fork the chain (master prompt: "the authoritative
-- concurrency guarantee must exist at the database/transaction level",
-- not an application-level mutex, which would do nothing across
-- multiple backend processes/pooled connections anyway). The key is an
-- arbitrary, fixed constant (see chain.go's auditChainLockKey) reserved
-- solely for this purpose — it names no row and touches no other lock
-- table.
SELECT pg_advisory_xact_lock(sqlc.arg(lock_key)::bigint);

-- name: GetLatestAuditEntry :one
-- The current chain head — the writer reads this (AFTER acquiring the
-- advisory lock above, within the same transaction) to learn the
-- prev_hash for the next entry it constructs. Backed by
-- audit_log_seq_unique's implicit index — an index-only backward scan
-- for LIMIT 1, never a full table scan.
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
-- start from the genesis entry) — the batched read chain verification
-- uses, so verifying a multi-million-row chain never requires loading it
-- all into memory at once.
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

-- name: ListAuditEntriesFiltered :many
-- GET /audit's real query: every filter optional (NULL = "no constraint
-- on this field") — same convention as ListCasesFiltered/
-- ListUsersFiltered. This runs under the CALLER's own RLS identity (see
-- internal/service.AuditService.List) — audit_log_select's policy
-- (ADMIN, or own user_id, or a case the caller is a member of) narrows
-- the result set independently of, and beneath, these filters: a filter
-- can never widen what RLS already restricts, only narrow it further.
SELECT
    id, seq, "timestamp", user_id, role, action, resource_type, resource_id,
    case_id, metadata, prev_hash, hash
FROM audit_log
WHERE (sqlc.narg(user_id)::uuid IS NULL OR user_id = sqlc.narg(user_id))
  AND (sqlc.narg(role)::text IS NULL OR role = sqlc.narg(role))
  AND (sqlc.narg(action)::text IS NULL OR action = sqlc.narg(action))
  AND (sqlc.narg(resource_type)::text IS NULL OR resource_type = sqlc.narg(resource_type))
  AND (sqlc.narg(resource_id)::uuid IS NULL OR resource_id = sqlc.narg(resource_id))
  AND (sqlc.narg(case_id)::uuid IS NULL OR case_id = sqlc.narg(case_id))
  AND (sqlc.narg(from_ts)::timestamptz IS NULL OR "timestamp" >= sqlc.narg(from_ts))
  AND (sqlc.narg(to_ts)::timestamptz IS NULL OR "timestamp" < sqlc.narg(to_ts))
ORDER BY seq DESC
LIMIT sqlc.arg(limit_val) OFFSET sqlc.arg(offset_val);

-- name: CountAuditEntriesFiltered :one
-- Same filters as ListAuditEntriesFiltered — the caller's authorized,
-- filtered total for pagination metadata, not an unfiltered table count.
SELECT count(*) FROM audit_log
WHERE (sqlc.narg(user_id)::uuid IS NULL OR user_id = sqlc.narg(user_id))
  AND (sqlc.narg(role)::text IS NULL OR role = sqlc.narg(role))
  AND (sqlc.narg(action)::text IS NULL OR action = sqlc.narg(action))
  AND (sqlc.narg(resource_type)::text IS NULL OR resource_type = sqlc.narg(resource_type))
  AND (sqlc.narg(resource_id)::uuid IS NULL OR resource_id = sqlc.narg(resource_id))
  AND (sqlc.narg(case_id)::uuid IS NULL OR case_id = sqlc.narg(case_id))
  AND (sqlc.narg(from_ts)::timestamptz IS NULL OR "timestamp" >= sqlc.narg(from_ts))
  AND (sqlc.narg(to_ts)::timestamptz IS NULL OR "timestamp" < sqlc.narg(to_ts));

-- name: CountAuditEntries :one
-- The chain's total row count — used by chain verification to report
-- total_entries alongside entries_checked, and cheap (a single index/
-- heap estimate-free count) since it is only ever called once per
-- verification request, never per-row.
SELECT count(*) FROM audit_log;
