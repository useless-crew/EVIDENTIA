-- Evidentia — Blockchain Anchor Queries (System 20)
--
-- These queries manage the lifecycle of blockchain_anchors rows: creation
-- (after a document event that should be anchored), status updates (as the
-- Asynq worker progresses), and retrieval (for API responses and provenance
-- queries). Evidence content is never read or written here.

-- name: CreateBlockchainAnchor :one
-- Creates a PENDING anchor record immediately after a document event
-- (e.g. DOCUMENT_UPLOADED). The Asynq worker will pick this up and
-- submit the anchor to Hyperledger Fabric.
INSERT INTO blockchain_anchors (
    document_id,
    document_version,
    event_type,
    document_hash,
    fabric_channel,
    fabric_chaincode,
    organization
) VALUES (
    $1, $2, $3, $4, $5, $6, $7
)
RETURNING
    id, document_id, document_version, event_type, document_hash,
    status, fabric_tx_id, fabric_channel, fabric_chaincode, organization,
    last_error, retry_count, metadata, created_at, confirmed_at, updated_at;

-- name: GetBlockchainAnchorByID :one
-- Retrieves a single anchor record by its ID. Used by the API to return
-- anchor status and by the Asynq worker to load the pending anchor.
SELECT
    id, document_id, document_version, event_type, document_hash,
    status, fabric_tx_id, fabric_channel, fabric_chaincode, organization,
    last_error, retry_count, metadata, created_at, confirmed_at, updated_at
FROM blockchain_anchors
WHERE id = $1;

-- name: GetLatestBlockchainAnchorByDocumentID :one
-- Returns the most recent anchor for a given document, regardless of
-- status. Used by document-viewer and verification APIs to show the
-- current blockchain state of a document.
SELECT
    id, document_id, document_version, event_type, document_hash,
    status, fabric_tx_id, fabric_channel, fabric_chaincode, organization,
    last_error, retry_count, metadata, created_at, confirmed_at, updated_at
FROM blockchain_anchors
WHERE document_id = $1
ORDER BY created_at DESC
LIMIT 1;

-- name: ListBlockchainAnchorsByDocumentID :many
-- Returns all anchors for a document, ordered by creation time (oldest
-- first). Used by the provenance/history API to show the full anchoring
-- timeline including all versions and event types.
SELECT
    id, document_id, document_version, event_type, document_hash,
    status, fabric_tx_id, fabric_channel, fabric_chaincode, organization,
    last_error, retry_count, metadata, created_at, confirmed_at, updated_at
FROM blockchain_anchors
WHERE document_id = $1
ORDER BY created_at ASC;

-- name: ConfirmBlockchainAnchor :one
-- Marks an anchor CONFIRMED after the Fabric transaction has been
-- committed. Only transitions from PENDING to avoid double-confirmation.
-- The `AND status = 'PENDING'` guard makes this safe against concurrent
-- worker retries: a second attempt to confirm an already-CONFIRMED row
-- matches zero rows (pgx.ErrNoRows), which the worker treats as a
-- no-op (the work was already done).
UPDATE blockchain_anchors
SET
    status          = 'CONFIRMED',
    fabric_tx_id    = $2,
    organization    = $3,
    confirmed_at    = now(),
    updated_at      = now()
WHERE id = $1
  AND status = 'PENDING'
RETURNING
    id, document_id, document_version, event_type, document_hash,
    status, fabric_tx_id, fabric_channel, fabric_chaincode, organization,
    last_error, retry_count, metadata, created_at, confirmed_at, updated_at;

-- name: FailBlockchainAnchor :one
-- Marks an anchor FAILED after all retry attempts are exhausted.
-- Records the last error for observability.
UPDATE blockchain_anchors
SET
    status      = 'FAILED',
    last_error  = $2,
    retry_count = $3,
    updated_at  = now()
WHERE id = $1
  AND status = 'PENDING'
RETURNING
    id, document_id, document_version, event_type, document_hash,
    status, fabric_tx_id, fabric_channel, fabric_chaincode, organization,
    last_error, retry_count, metadata, created_at, confirmed_at, updated_at;

-- name: IncrementBlockchainAnchorRetry :exec
-- Records a transient retry attempt. Called by the worker before
-- re-enqueueing so the retry_count reflects how many attempts have
-- been made, for observability.
UPDATE blockchain_anchors
SET retry_count = retry_count + 1, last_error = $2, updated_at = now()
WHERE id = $1 AND status = 'PENDING';

-- name: CountPendingBlockchainAnchors :one
-- Returns the count of PENDING anchors — used by the health/monitoring
-- endpoint to detect stalled anchors.
SELECT count(*) FROM blockchain_anchors WHERE status = 'PENDING';
