package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
)

// TypeBlockchainAnchor identifies System 20's blockchain anchoring task type.
// One task per anchor row — the worker loads the full anchor from PostgreSQL
// (via the payload's AnchorID) rather than embedding evidence data in the
// payload (master prompt §13's "idempotency" and "server-derived data"
// principles applied to blockchain tasks, mirroring VerifyAuditChainPayload's
// identical rationale).
const TypeBlockchainAnchor = "blockchain:anchor"

// blockchainAnchorMaxRetry/Timeout: retry budget for TRANSIENT failures
// (Fabric unavailable, network partition). Permanent failures (chaincode
// rejection, invalid identity, malformed input) return nil from ProcessTask
// and are never retried — see BlockchainAnchorHandler.ProcessTask's own
// doc comment. 5 retries over 30 seconds max per attempt gives roughly
// a 2-minute total retry window before the job is declared failed.
const (
	blockchainAnchorMaxRetry = 5
	blockchainAnchorTimeout  = 30 * time.Second
)

// BlockchainAnchorPayload carries only the anchor ID. The worker loads
// everything else from PostgreSQL — no evidence content, no hashes, no
// credentials in the Asynq/Redis payload (master prompt §13/§33).
type BlockchainAnchorPayload struct {
	AnchorID uuid.UUID `json:"anchor_id"`
}

// BlockchainAnchorJobID derives the deterministic Asynq task ID for a given
// anchor — see DeterministicTaskID's doc comment. Exported so
// BlockchainAnchorService can populate the job_id field without re-deriving
// the string.
func BlockchainAnchorJobID(anchorID uuid.UUID) string {
	return DeterministicTaskID(TypeBlockchainAnchor, anchorID)
}

// NewBlockchainAnchorTask builds the Asynq task that submits one PENDING
// anchor to the Fabric ledger. Uses QueueDefault (priority=2) — blockchain
// anchoring is important but not as time-sensitive as audit-chain integrity
// verification (QueueCritical), which must never be starved by a burst of
// upload-triggered anchor jobs. Priority may be revised as operational
// experience accumulates.
func NewBlockchainAnchorTask(anchorID uuid.UUID) (*asynq.Task, error) {
	payload, err := json.Marshal(BlockchainAnchorPayload{AnchorID: anchorID})
	if err != nil {
		return nil, fmt.Errorf("jobs: marshal blockchain-anchor payload: %w", err)
	}
	return asynq.NewTask(
		TypeBlockchainAnchor,
		payload,
		asynq.MaxRetry(blockchainAnchorMaxRetry),
		asynq.Timeout(blockchainAnchorTimeout),
		asynq.Queue(QueueDefault),
		asynq.TaskID(BlockchainAnchorJobID(anchorID)),
	), nil
}

// EnqueueBlockchainAnchor is the one call BlockchainAnchorService makes to
// dispatch async Fabric submission after creating a PENDING anchor row.
func (c *Client) EnqueueBlockchainAnchor(ctx context.Context, anchorID uuid.UUID) error {
	task, err := NewBlockchainAnchorTask(anchorID)
	if err != nil {
		return err
	}
	return c.enqueue(ctx, task)
}

// BlockchainAnchorer is the narrow capability BlockchainAnchorHandler needs
// — satisfied structurally by *service.BlockchainAnchorService (which this
// package never imports, avoiding the cycle that mirrors jobs→service already
// established by AuditVerifier/AuditVerificationHandler). RunAnchor must
// return nil for both CONFIRMED and FAILED terminal outcomes; only a genuine
// transient operational failure should return an error (triggering Asynq
// retry).
type BlockchainAnchorer interface {
	RunAnchor(ctx context.Context, anchorID uuid.UUID) error
}

// BlockchainAnchorFailureRecorder is the narrow capability the error handler
// needs to mark an anchor row FAILED after Asynq's retry budget is exhausted.
type BlockchainAnchorFailureRecorder interface {
	MarkAnchorOperationallyFailed(ctx context.Context, anchorID uuid.UUID, cause error) error
}

// BlockchainAnchorHandler adapts BlockchainAnchorer to asynq.Handler.
type BlockchainAnchorHandler struct {
	anchorer BlockchainAnchorer
}

func NewBlockchainAnchorHandler(anchorer BlockchainAnchorer) *BlockchainAnchorHandler {
	return &BlockchainAnchorHandler{anchorer: anchorer}
}

// ProcessTask implements asynq.Handler. Permanent failures return nil (so
// Asynq marks the task done); transient failures return an error (so Asynq
// retries within blockchainAnchorMaxRetry). LoggingMiddleware (NewMux) is
// the sole logging point for start/duration/failure — never double-logged
// here.
func (h *BlockchainAnchorHandler) ProcessTask(ctx context.Context, task *asynq.Task) error {
	var payload BlockchainAnchorPayload
	if err := json.Unmarshal(task.Payload(), &payload); err != nil {
		return Permanent(FailureCategoryPermanent, fmt.Errorf("jobs: unmarshal blockchain-anchor payload: %w", err))
	}
	return h.anchorer.RunAnchor(ctx, payload.AnchorID)
}

// NewBlockchainAnchorErrorHandler returns an asynq.ErrorHandler that marks
// an anchor row FAILED only after Asynq's retry budget is exhausted —
// mirroring NewAuditVerificationErrorHandler's identical semantics for
// transient failure handling.
func NewBlockchainAnchorErrorHandler(recorder BlockchainAnchorFailureRecorder) asynq.ErrorHandler {
	return asynq.ErrorHandlerFunc(func(ctx context.Context, task *asynq.Task, err error) {
		if task.Type() != TypeBlockchainAnchor {
			return
		}
		retried, _ := asynq.GetRetryCount(ctx)
		maxRetry, _ := asynq.GetMaxRetry(ctx)
		if !isRetriesExhausted(retried, maxRetry, err) {
			return
		}

		var payload BlockchainAnchorPayload
		if unmarshalErr := json.Unmarshal(task.Payload(), &payload); unmarshalErr != nil {
			return
		}

		_ = recorder.MarkAnchorOperationallyFailed(ctx, payload.AnchorID, err)
	})
}
