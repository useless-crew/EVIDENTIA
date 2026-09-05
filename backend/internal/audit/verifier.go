package audit

import (
	"bytes"

	"github.com/google/uuid"

	"evidentia/backend/db/generated"
)

// Chain verification result statuses — the entire vocabulary a
// verification call can report. Deliberately not a database column: a
// verification is a computed-on-request comparison over the existing
// hash chain, never persisted state (mirrors System 7's
// VerificationStatusVerified/IntegrityFailure for document hashes —
// the identical "both are a successful, meaningful result, never
// confused with a request FAILURE" posture, just applied to the audit
// chain itself here).
const (
	VerificationStatusVerified         = "VERIFIED"
	VerificationStatusIntegrityFailure = "INTEGRITY_FAILURE"

	// VerificationStatusQueued/Running are System 11's async job-lifecycle
	// states — a verification exists in one of these two BEFORE it reaches
	// one of the two terminal outcomes above, or the operational-failure
	// terminal state below. Defined here, not in internal/service or
	// internal/jobs, so the complete status vocabulary for "what a
	// verification's status column may ever contain" has one home,
	// alongside the outcome constants it precedes.
	VerificationStatusQueued  = "QUEUED"
	VerificationStatusRunning = "RUNNING"

	// VerificationStatusFailed is an OPERATIONAL failure (PostgreSQL
	// unavailable, an unexpected driver/storage error, a worker timeout) —
	// verification could not complete at all, which is materially
	// different from VerificationStatusIntegrityFailure (verification DID
	// complete and found a cryptographic/structural problem). Never use
	// one to report the other: an outage is not evidence of tampering, and
	// a definite tamper finding is not a transient error to retry away.
	VerificationStatusFailed = "FAILED"
)

// Failure-type categories for VerificationStatusIntegrityFailure — see
// BatchResult.FailureType. This is deliberately the SMALLEST set VerifyBatch
// can distinguish with certainty from a linear, seq-ordered scan, not the
// full vocabulary a threat model might imagine:
//
//   - CHAIN_FORK_DETECTED / DUPLICATE_ENTRY are never produced here because
//     they cannot exist in a successfully committed chain in the first
//     place: idx_audit_log_prev_hash_unique/idx_audit_log_single_genesis
//     (see db/migrations/000001_init_schema.up.sql) reject a forking or
//     duplicate-genesis INSERT at the database level before it can ever
//     commit — a verifier scanning committed rows structurally cannot
//     observe a fork attempt's non-existence as a distinct symptom from a
//     plain broken link.
//   - CHAIN_ORDER_INVALID / MISSING_ENTRY are not separate categories
//     either: audit_log.seq is a GENERATED ALWAYS AS IDENTITY column
//     (monotonic by construction — ORDER BY seq IS chain order, nothing to
//     misorder), and a deleted middle entry is caught as, and reported
//     identically to, FailureTypePreviousHashMismatch (the next surviving
//     entry's prev_hash no longer matches anything) — this is exactly what
//     it structurally IS: a broken link, whatever real-world event caused
//     it. Inventing a more specific label here would be false precision
//     the algorithm cannot actually back up.
//
// See docs/AUDIT_CHAIN.md's "Verification" section for the full reasoning.
const (
	FailureTypeGenesisInvalid        = "GENESIS_INVALID"
	FailureTypePreviousHashMismatch  = "PREVIOUS_HASH_MISMATCH"
	FailureTypeEntryHashMismatch     = "ENTRY_HASH_MISMATCH"
	FailureTypeCanonicalizationError = "CANONICALIZATION_ERROR"
)

// Failure-type categories for VerificationStatusFailed — an operational
// failure, never a cryptographic finding. Populated by internal/service/
// internal/jobs (this package has no database/network/timeout concerns of
// its own to classify), listed here purely so the two vocabularies
// (integrity findings above, operational failures below) live in one place
// and are visibly, deliberately never mixed.
const (
	OperationalFailureDatabaseError = "DATABASE_ERROR"
	OperationalFailureTimeout       = "TIMEOUT"
	// OperationalFailureStaleTimeout marks a QUEUED/RUNNING verification
	// whose worker went silent (no progress update) long enough to be
	// presumed dead — e.g. the process was killed outright, never reaching
	// its own completion/failure handling. Detected and applied lazily, at
	// READ time, by internal/service.AuditService — see that type's
	// reconcileStale doc comment for why no separate sweeper process
	// exists for this.
	OperationalFailureStaleTimeout = "STALE_TIMEOUT"
)

// BatchResult is the outcome of verifying one ordered, contiguous batch
// of chain entries against an expected starting prev_hash (the previous
// batch's own LastHash, or nil for the very first batch — genesis is
// expected at entries[0] in that case). EntriesChecked counts entries
// that were confirmed valid BEFORE any failure (== len(entries) on
// success); the Failed*/Expected*/Actual* fields are populated only when
// OK is false, and deliberately carry no metadata content or other
// sensitive payload — only identifiers and hashes, safe to return to an
// API client per master prompt's "do not expose sensitive document
// contents or secrets" for the verification endpoint.
type BatchResult struct {
	OK             bool
	EntriesChecked int
	LastHash       []byte // valid only when OK — the new expected prev_hash for the NEXT batch
	FailedEntryID  *uuid.UUID
	FailedSeq      *int64
	Reason         string
	// FailureType is one of the FailureType* constants above — set
	// whenever OK is false, alongside the free-text Reason (kept for
	// human-readable detail; FailureType is the stable, machine-readable
	// category System 11 persists to audit_verifications.failure_type and
	// exposes over the API).
	FailureType      string
	ExpectedPrevHash []byte
	ActualPrevHash   []byte
	ExpectedHash     []byte
	ActualHash       []byte
}

// VerifyBatch verifies entries — already loaded in ascending seq order,
// a single contiguous page from ListAuditEntriesFromSeq — against
// expectedPrevHash. It is pure and side-effect-free (no database access,
// no I/O): callers (internal/service.AuditService.VerifyChain) supply
// successive pages and thread LastHash from one call into the next
// call's expectedPrevHash, so a chain of any size verifies in bounded
// memory — this function never needs to see more than one page's worth
// of rows at a time.
//
// Note that seq itself is allowed to have GAPS between consecutive rows
// (PostgreSQL's GENERATED ALWAYS AS IDENTITY consumes a sequence value
// even for a since-rolled-back insert attempt — entirely normal, not a
// sign of tampering) — this function never assumes consecutive integers,
// only that each row's prev_hash equals the IMMEDIATELY PRECEDING ROW's
// own hash, which is the actual chain-integrity invariant that matters.
func VerifyBatch(entries []generated.AuditLog, expectedPrevHash []byte) BatchResult {
	prev := expectedPrevHash

	for i, e := range entries {
		if !bytes.Equal(e.PrevHash, prev) {
			// Either side of the comparison being nil (never both — that
			// case IS bytes.Equal and would not have reached here) means
			// this is a genesis-shaped mismatch: an entry that should be
			// genesis claims a real predecessor, or an unexpected second
			// genesis-shaped entry appears mid-chain. Anything else is a
			// plain broken link between two otherwise-ordinary entries —
			// see the FailureType* constants' doc comment for why this is
			// the most specific classification the algorithm can honestly
			// make (this single category also covers a deleted entry and
			// an attempted-but-never-committed fork).
			failureType := FailureTypePreviousHashMismatch
			if prev == nil || e.PrevHash == nil {
				failureType = FailureTypeGenesisInvalid
			}
			return BatchResult{
				EntriesChecked:   i,
				FailedEntryID:    &e.ID,
				FailedSeq:        e.Seq,
				Reason:           "prev_hash does not match the preceding entry's hash — chain link broken",
				FailureType:      failureType,
				ExpectedPrevHash: prev,
				ActualPrevHash:   e.PrevHash,
			}
		}

		canonicalMetadata, err := CanonicalizeMetadata(e.Metadata)
		if err != nil {
			return BatchResult{
				EntriesChecked: i,
				FailedEntryID:  &e.ID,
				FailedSeq:      e.Seq,
				Reason:         "stored metadata could not be canonicalized: " + err.Error(),
				FailureType:    FailureTypeCanonicalizationError,
			}
		}

		expectedHash := ComputeEntryHash(Entry{
			ID:           e.ID,
			Timestamp:    e.Timestamp,
			UserID:       e.UserID,
			Role:         roleOrEmpty(e.Role),
			Action:       e.Action,
			ResourceType: e.ResourceType,
			ResourceID:   e.ResourceID,
			CaseID:       e.CaseID,
			Metadata:     canonicalMetadata,
			PrevHash:     e.PrevHash,
		})
		if !bytes.Equal(expectedHash, e.Hash) {
			return BatchResult{
				EntriesChecked:   i,
				FailedEntryID:    &e.ID,
				FailedSeq:        e.Seq,
				Reason:           "recomputed entry hash does not match the stored hash — entry contents modified",
				FailureType:      FailureTypeEntryHashMismatch,
				ExpectedHash:     expectedHash,
				ActualHash:       e.Hash,
				ExpectedPrevHash: prev,
				ActualPrevHash:   e.PrevHash,
			}
		}

		prev = e.Hash
	}

	return BatchResult{OK: true, EntriesChecked: len(entries), LastHash: prev}
}

func roleOrEmpty(role *string) string {
	if role == nil {
		return ""
	}
	return *role
}
