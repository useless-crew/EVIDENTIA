// Package realtime is System 11's Server-Sent Events transport: an
// in-process, per-verification pub/sub (Broadcaster) and the Gin handler
// that streams it to an authenticated client (sse.go). It knows nothing
// about hashing, PostgreSQL, or Asynq — internal/service publishes events
// here as a side effect of running a verification; this package only ever
// fans them out to whoever is currently subscribed.
package realtime

import (
	"time"

	"github.com/google/uuid"
)

// Verification SSE event types — the `event:` field of each frame (see
// sse.go). Named to match master prompt's suggested vocabulary exactly.
const (
	EventVerificationStarted          = "verification_started"
	EventVerificationProgress         = "verification_progress"
	EventVerificationCompleted        = "verification_completed"
	EventVerificationIntegrityFailure = "verification_integrity_failure"
	EventVerificationFailed           = "verification_failed"
)

// VerificationEvent is one progress/outcome update for a single
// verification run — a safe, progress-focused SUBSET of the fields
// GET /audit/verify-chain/:id returns (internal/service.
// VerificationDetail), never a request-metadata-carrying full mirror of
// it (no requested_by_user_id/role, no created_at/updated_at — nothing an
// intermediate progress tick needs). Critically, every field this DOES
// carry uses the EXACT SAME name as its VerificationDetail counterpart —
// never renamed or reshaped — so an SSE client reconnecting and falling
// back to a REST GET (see docs/AUDIT_CHAIN.md's "SSE reconnection") can
// reuse the same field-access code for both without a translation layer.
// Never carries audit_log metadata, document content, or any secret —
// only identifiers, counts, and a classified failure type/reason,
// mirroring internal/service.ChainVerificationResult's own established
// safety posture from System 10.
type VerificationEvent struct {
	Type           string     `json:"type"`
	VerificationID uuid.UUID  `json:"verification_id"`
	Status         string     `json:"status"`
	EntriesChecked int64      `json:"entries_checked"`
	TotalEntries   *int64     `json:"total_entries,omitempty"`
	ProgressPct    *float64   `json:"progress_percent,omitempty"`
	FailedEntryID  *uuid.UUID `json:"failed_entry_id,omitempty"`
	FailureType    string     `json:"failure_type,omitempty"`
	// FailureReason deliberately matches VerificationDetail's own
	// `failure_reason` JSON field name (never a differently-named
	// `reason`) — see this type's own doc comment: the two transports
	// must never disagree on what a field is called for the same fact.
	FailureReason string    `json:"failure_reason,omitempty"`
	Timestamp     time.Time `json:"timestamp"`
}

// Terminal reports whether this event represents the end of a
// verification's stream — the SSE handler closes the connection right
// after sending one of these (see sse.go).
func (e VerificationEvent) Terminal() bool {
	switch e.Type {
	case EventVerificationCompleted, EventVerificationIntegrityFailure, EventVerificationFailed:
		return true
	default:
		return false
	}
}
