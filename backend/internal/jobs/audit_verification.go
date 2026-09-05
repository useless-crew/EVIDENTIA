package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
)

// TypeVerifyAuditChain identifies System 11's one background task type.
const TypeVerifyAuditChain = "audit:verify_chain"

// verifyAuditChainMaxRetry/Timeout bound an operational-failure retry
// window and a single attempt's maximum duration respectively. Retries are
// for OPERATIONAL failures only (a transient database error) — see
// audit_verification.go's ErrorHandler and internal/audit.
// VerificationStatusFailed's own doc comment for why a cryptographic
// INTEGRITY_FAILURE must never trigger a retry: ProcessTask returns nil
// (success, from Asynq's point of view — the job DID complete) for that
// outcome, exactly like it does for VERIFIED.
const (
	verifyAuditChainMaxRetry = 3
	verifyAuditChainTimeout  = 30 * time.Minute
)

// VerifyAuditChainPayload is the ENTIRE input a verification task carries —
// deliberately just an ID. The worker loads every other piece of
// information (which entries to check, what "correct" looks like) fresh
// from PostgreSQL itself; a client can never smuggle an expected hash,
// canonicalization rule, or verification outcome through this payload (see
// docs/AUDIT_CHAIN.md's "Audit Insert Authority" for the identical
// principle already applied to audit_log itself).
type VerifyAuditChainPayload struct {
	VerificationID uuid.UUID `json:"verification_id"`
}

// NewVerifyAuditChainTask builds the task internal/service.AuditService.
// StartVerification enqueues once it has created (or found an already-
// active) audit_verifications row.
func NewVerifyAuditChainTask(verificationID uuid.UUID) (*asynq.Task, error) {
	payload, err := json.Marshal(VerifyAuditChainPayload{VerificationID: verificationID})
	if err != nil {
		return nil, fmt.Errorf("jobs: marshal verify-audit-chain payload: %w", err)
	}
	return asynq.NewTask(
		TypeVerifyAuditChain,
		payload,
		asynq.MaxRetry(verifyAuditChainMaxRetry),
		asynq.Timeout(verifyAuditChainTimeout),
		asynq.Queue("default"),
	), nil
}

// EnqueueVerifyAuditChain is the one call internal/service.AuditService.
// StartVerification makes to actually dispatch background verification.
func (c *Client) EnqueueVerifyAuditChain(ctx context.Context, verificationID uuid.UUID) error {
	task, err := NewVerifyAuditChainTask(verificationID)
	if err != nil {
		return err
	}
	return c.enqueue(ctx, task)
}

// AuditVerifier is the narrow capability AuditVerificationHandler needs —
// satisfied structurally by *service.AuditService (which this package
// never imports; see this file's package doc comment). RunVerification
// must NEVER return an error for a completed VERIFIED or
// INTEGRITY_FAILURE result — both are a successful task execution from
// Asynq's point of view. An error return means the run could not complete
// at all (an operational failure), which is the ONLY case Asynq should
// retry.
type AuditVerifier interface {
	RunVerification(ctx context.Context, verificationID uuid.UUID) error
}

// AuditFailureRecorder is the narrow capability the ErrorHandler below
// needs to record a verification as terminally FAILED once Asynq's own
// retry budget for it is exhausted — see NewAuditVerificationErrorHandler.
type AuditFailureRecorder interface {
	MarkVerificationOperationallyFailed(ctx context.Context, verificationID uuid.UUID, cause error) error
}

// AuditVerificationHandler adapts AuditVerifier to asynq.Handler.
type AuditVerificationHandler struct {
	verifier AuditVerifier
	logger   *slog.Logger
}

func NewAuditVerificationHandler(verifier AuditVerifier, logger *slog.Logger) *AuditVerificationHandler {
	return &AuditVerificationHandler{verifier: verifier, logger: logger}
}

// ProcessTask implements asynq.Handler. It never touches PostgreSQL/hash
// logic itself — see AuditVerifier's doc comment; this method's entire job
// is unmarshaling the payload and translating a returned error into
// Asynq's own retry mechanism.
func (h *AuditVerificationHandler) ProcessTask(ctx context.Context, task *asynq.Task) error {
	var payload VerifyAuditChainPayload
	if err := json.Unmarshal(task.Payload(), &payload); err != nil {
		// A malformed payload can never succeed on retry — asynq.SkipRetry
		// tells Asynq not to waste its retry budget on it.
		return fmt.Errorf("jobs: unmarshal verify-audit-chain payload: %w: %w", err, asynq.SkipRetry)
	}

	if err := h.verifier.RunVerification(ctx, payload.VerificationID); err != nil {
		h.logger.ErrorContext(ctx, "audit chain verification task failed",
			slog.String("verification_id", payload.VerificationID.String()),
			slog.String("error", err.Error()),
		)
		return err
	}
	return nil
}

// NewAuditVerificationErrorHandler returns an asynq.ErrorHandler that
// marks a verification row FAILED only once Asynq has exhausted every
// retry attempt for it (asynq.GetRetryCount == asynq.GetMaxRetry) — never
// on an intermediate attempt, so a transient database blip that succeeds
// on retry 2 never leaves a stray FAILED row behind alongside the
// eventual real result. This is the mechanism behind "retry according to
// existing Asynq configuration" for a genuinely operational failure,
// without ever retrying (or mis-marking) a cryptographic
// INTEGRITY_FAILURE, which ProcessTask above never surfaces as an error
// in the first place.
func NewAuditVerificationErrorHandler(recorder AuditFailureRecorder, logger *slog.Logger) asynq.ErrorHandler {
	return asynq.ErrorHandlerFunc(func(ctx context.Context, task *asynq.Task, err error) {
		if task.Type() != TypeVerifyAuditChain {
			return
		}
		retried, _ := asynq.GetRetryCount(ctx)
		maxRetry, _ := asynq.GetMaxRetry(ctx)
		if !isRetriesExhausted(retried, maxRetry, err) {
			return
		}

		var payload VerifyAuditChainPayload
		if unmarshalErr := json.Unmarshal(task.Payload(), &payload); unmarshalErr != nil {
			logger.ErrorContext(ctx, "audit verification error handler: cannot identify failed verification",
				slog.String("error", unmarshalErr.Error()))
			return
		}

		if markErr := recorder.MarkVerificationOperationallyFailed(ctx, payload.VerificationID, err); markErr != nil {
			logger.ErrorContext(ctx, "audit verification error handler: failed to persist FAILED status",
				slog.String("verification_id", payload.VerificationID.String()),
				slog.String("error", markErr.Error()),
			)
		}
	})
}

// isRetriesExhausted is the pure decision NewAuditVerificationErrorHandler
// delegates to — separated out specifically so it can be unit-tested
// without needing a real Asynq processing context (asynq.GetRetryCount/
// GetMaxRetry read from an unexported internal context key only asynq's
// own server machinery can populate, which isn't constructible from a
// plain unit test). A malformed-payload failure (asynq.SkipRetry) is
// always exhausted immediately — there is no retry count to wait out for
// an error asynq itself will never retry.
func isRetriesExhausted(retried, maxRetry int, err error) bool {
	if errors.Is(err, asynq.SkipRetry) {
		return true
	}
	return retried >= maxRetry
}
