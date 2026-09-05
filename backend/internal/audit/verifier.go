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
	OK               bool
	EntriesChecked   int
	LastHash         []byte // valid only when OK — the new expected prev_hash for the NEXT batch
	FailedEntryID    *uuid.UUID
	FailedSeq        *int64
	Reason           string
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
			return BatchResult{
				EntriesChecked:   i,
				FailedEntryID:    &e.ID,
				FailedSeq:        e.Seq,
				Reason:           "prev_hash does not match the preceding entry's hash — chain link broken",
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
