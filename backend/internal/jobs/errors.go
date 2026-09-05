package jobs

import (
	"errors"
	"fmt"

	"github.com/hibiken/asynq"
)

// FailureCategory classifies why a task handler's ProcessTask returned an
// error — the one vocabulary every task type in this package uses to
// decide whether Asynq should retry, and what an operator/observability
// consumer (LoggingMiddleware, a future job-history reader) sees when it
// doesn't. This is deliberately narrower than a full error taxonomy:
// System 12's own mandate is "classify TRANSIENT/PERMANENT/SECURITY/
// INTEGRITY and choose retry behavior accordingly" — nothing here decides
// retry COUNTS or backoff timing itself (that remains asynq.MaxRetry/
// asynq.Timeout, configured per task type exactly like
// verifyAuditChainMaxRetry/verifyAuditChainTimeout already are); this
// type only answers "should Asynq even attempt a retry at all".
type FailureCategory string

const (
	// FailureCategoryTransient is an operational failure expected to
	// succeed on retry — a momentary PostgreSQL/Redis/object-storage
	// blip, a network timeout. The default/zero classification: a plain,
	// unwrapped error returned from ProcessTask (the common case — see
	// AuditVerificationHandler.ProcessTask's operational-failure path)
	// is always treated as transient, because assuming "a retry might
	// help" is the safe default for an error this package was never told
	// otherwise about.
	FailureCategoryTransient FailureCategory = "TRANSIENT"
	// FailureCategoryPermanent is a failure no amount of retrying can
	// ever fix — a malformed task payload, a referenced resource that no
	// longer exists. Always paired with asynq.SkipRetry (see Permanent).
	FailureCategoryPermanent FailureCategory = "PERMANENT"
	// FailureCategorySecurity is a permanent failure specifically because
	// the operation was never authorized to happen — reserved for a
	// future task type whose worker itself detects an authorization
	// problem post-enqueue (e.g. the resource's ownership changed between
	// the API's own authorization check and the worker picking up the
	// task); no task type needs this today; the category exists so a
	// future one has a designated slot in this vocabulary rather than
	// overloading FailureCategoryPermanent for a materially different
	// reason (master prompt: "classify TRANSIENT/PERMANENT/SECURITY/
	// INTEGRITY").
	FailureCategorySecurity FailureCategory = "SECURITY"
	// FailureCategoryIntegrity marks a cryptographic/structural integrity
	// finding — System 11's INTEGRITY_FAILURE is the one example today.
	// Critically, this is NEVER how such a finding reaches Asynq:
	// internal/service.AuditService.RunVerification returns a nil error
	// for INTEGRITY_FAILURE, exactly like it does for VERIFIED (see that
	// method's own doc comment) — a definite tamper finding is a
	// SUCCESSFULLY COMPLETED task run, not a failure to retry away. This
	// constant is listed here purely so the complete classification
	// vocabulary master prompt's "Retry Policy" section names
	// (TRANSIENT/PERMANENT/SECURITY/INTEGRITY) lives in one place and is
	// visibly, deliberately never conflated with FailureCategoryPermanent
	// — an outage is not evidence of tampering, and a tamper finding is
	// not an operational error, in either direction.
	FailureCategoryIntegrity FailureCategory = "INTEGRITY"
)

// CategorizedError is an error a Handler.ProcessTask returns to attach an
// explicit FailureCategory to it — see Permanent, the one constructor
// this package exposes (a transient failure needs no wrapper at all: just
// return the plain error, which CategoryOf below already treats as
// FailureCategoryTransient by default).
type CategorizedError struct {
	Category FailureCategory
	Err      error
}

func (e *CategorizedError) Error() string { return e.Err.Error() }
func (e *CategorizedError) Unwrap() error { return e.Err }

// Permanent wraps err as a category-classified failure that Asynq must
// NEVER retry — combined with asynq.SkipRetry (via a standard multi-%w
// wrap, so errors.Is(result, asynq.SkipRetry) still finds it exactly like
// asynq's own processor checks for internally) so a Handler.ProcessTask
// can return the result of this call directly: retries stop immediately,
// AND the category survives for CategoryOf/observability to read back
// (LoggingMiddleware, a future error handler). category should be
// FailureCategoryPermanent or FailureCategorySecurity — never
// FailureCategoryTransient (contradicts the entire point of this
// function) or FailureCategoryIntegrity (a cryptographic finding is never
// an error return at all — see that constant's own doc comment).
func Permanent(category FailureCategory, err error) error {
	return &CategorizedError{Category: category, Err: fmt.Errorf("%w: %w", err, asynq.SkipRetry)}
}

// CategoryOf reports the FailureCategory a Handler.ProcessTask error
// carries — FailureCategoryPermanent/FailureCategorySecurity for a
// Permanent(...)-wrapped error, FailureCategoryPermanent for a bare
// asynq.SkipRetry (a task type that skips retry without going through
// this package's own Permanent helper — e.g. a raw asynq.SkipRetry
// returned directly), and FailureCategoryTransient for anything else —
// the safe default for an error this package was never told a category
// for. Used by LoggingMiddleware to log a safe, structured failure
// category alongside every failed task, never the raw error text's own
// classification guesswork duplicated ad hoc per task type.
func CategoryOf(err error) FailureCategory {
	if err == nil {
		return ""
	}
	var categorized *CategorizedError
	if errors.As(err, &categorized) {
		return categorized.Category
	}
	if errors.Is(err, asynq.SkipRetry) {
		return FailureCategoryPermanent
	}
	return FailureCategoryTransient
}
