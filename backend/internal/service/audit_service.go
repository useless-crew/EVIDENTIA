// Package service (this file): the audit trail's orchestration layer —
// authorization, pagination, and response-shaping for GET /audit and
// POST /audit/verify-chain. All cryptographic logic (canonicalization,
// hashing, chain-write serialization, batch verification) lives in
// internal/audit (chain.go/writer.go/verifier.go), never here — this
// type only calls into that package and the repository layer, exactly
// like every other *Service in this file's package coordinates business
// logic on top of lower-level primitives (compare
// CertificateService/pkg/crypto).
package service

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"evidentia/backend/db/generated"
	auditpkg "evidentia/backend/internal/audit"
	"evidentia/backend/internal/auth"
	"evidentia/backend/internal/authz"
	"evidentia/backend/internal/repository"
	"evidentia/backend/internal/utils"
)

// defaultVerifyBatchSize bounds how many rows a single database round
// trip fetches during chain verification — the actual memory-bounding
// mechanism master prompt's "must not require loading millions of audit
// entries into RAM" requires. Independent of maxVerifyEntriesPerRequest
// below (that bounds one HTTP call's total work; this bounds one
// individual read).
const defaultVerifyBatchSize = 1000

// maxVerifyEntriesPerRequest is the default cap on how many entries ONE
// POST /audit/verify-chain call processes before returning (with
// NextSeq set so the caller can resume) — keeps a single HTTP request
// bounded in duration even against a very large chain, without
// introducing a background job/async architecture (explicitly out of
// scope — see docs/SECURITY.md's "Audit Trail" section). A caller
// verifying a chain smaller than this never even notices the cap: the
// whole chain is verified in one call, NextSeq is absent, and Status is
// final.
const maxVerifyEntriesPerRequest = 200_000

// AuditEntrySummary is one audit_log row shaped for API responses.
// Hashes are hex-encoded (this project's one established convention for
// exposing a binary digest at the API/JSON boundary — see
// pkg/hash.SumHex) — never raw bytes, never base64. PrevHash is empty
// only for the genesis entry.
type AuditEntrySummary struct {
	ID           uuid.UUID       `json:"id"`
	Seq          int64           `json:"seq"`
	Timestamp    time.Time       `json:"timestamp"`
	UserID       *uuid.UUID      `json:"user_id,omitempty"`
	Role         *string         `json:"role,omitempty"`
	Action       string          `json:"action"`
	ResourceType string          `json:"resource_type"`
	ResourceID   *uuid.UUID      `json:"resource_id,omitempty"`
	CaseID       *uuid.UUID      `json:"case_id,omitempty"`
	Metadata     json.RawMessage `json:"metadata"`
	PrevHash     string          `json:"prev_hash,omitempty"`
	Hash         string          `json:"hash"`
}

// AuditListFilter is GET /audit's optional filter set — every field NULL
// means "no constraint", mirroring CaseListFilter/AdminUserListFilter's
// existing convention. Note that a filter can only ever NARROW what the
// caller's own RLS identity already permits (audit_log_select) — it can
// never widen it: a LAWYER filtering by an arbitrary user_id they are
// unrelated to simply gets zero rows, never another user's history.
type AuditListFilter struct {
	UserID       *uuid.UUID
	Role         *string
	Action       *string
	ResourceType *string
	ResourceID   *uuid.UUID
	CaseID       *uuid.UUID
	From         *time.Time
	To           *time.Time
}

// AuditListResult is GET /audit's response data.
type AuditListResult struct {
	Entries []AuditEntrySummary `json:"entries"`
	Meta    utils.Meta          `json:"meta"`
}

// ChainVerificationResult is POST /audit/verify-chain's response data.
// Status is VERIFIED or INTEGRITY_FAILURE — both a SUCCESSFUL response
// (200), never an error: verification ANSWERING the question is
// success, exactly like System 7's document-hash VerificationResult.
// The Failed*/Expected*/Actual* fields are populated only on
// INTEGRITY_FAILURE, and deliberately carry no metadata/content — only
// identifiers and hex-encoded hashes (master prompt: never expose
// sensitive document contents or secrets from this endpoint).
type ChainVerificationResult struct {
	Status           string     `json:"status"`
	EntriesChecked   int64      `json:"entries_checked"`
	TotalEntries     int64      `json:"total_entries"`
	NextSeq          *int64     `json:"next_seq,omitempty"`
	FailedEntryID    *uuid.UUID `json:"failed_entry_id,omitempty"`
	FailedSeq        *int64     `json:"failed_seq,omitempty"`
	Reason           string     `json:"reason,omitempty"`
	ExpectedPrevHash string     `json:"expected_prev_hash,omitempty"`
	ActualPrevHash   string     `json:"actual_prev_hash,omitempty"`
	ExpectedHash     string     `json:"expected_hash,omitempty"`
	ActualHash       string     `json:"actual_hash,omitempty"`
	VerifiedAt       time.Time  `json:"verified_at"`
}

// AuditService owns audit-trail retrieval and chain verification.
// Deliberately does NOT own audit event RECORDING — every other service
// in this codebase already records its own events directly through the
// shared audit.Recorder (internal/audit.ChainWriter, once wired into
// app.New — see that type's own doc comment for why no existing caller
// needs to change). This type exists for the two read-side operations
// only: GET /audit and POST /audit/verify-chain.
type AuditService struct {
	pool     *pgxpool.Pool
	authz    *authz.Service
	recorder auditpkg.Recorder
}

func NewAuditService(pool *pgxpool.Pool, authzService *authz.Service, recorder auditpkg.Recorder) *AuditService {
	return &AuditService{pool: pool, authz: authzService, recorder: recorder}
}

// List authorizes user for audit:read (RBAC only — audit_log has no
// document/case-scoped ABAC resource to additionally check the way
// CanAccessDocument does; row-level visibility is entirely RLS's job
// here, per audit_log_select), then returns the filtered, paginated
// audit trail visible to that caller. Per audit_log_select: ADMIN sees
// everything; every other role sees only their OWN actions, plus any
// entry tied to a case they are an active member of — a LAWYER can
// never retrieve another case's (or another agency/user's, in this
// schema's terms: another unrelated case's) audit history no matter what
// filter values they supply, because RLS filters the underlying rows
// before this method's own filters are ever applied on top.
//
// Records its own AUDIT_ACCESSED event once the query has run — this
// cannot recurse: audit.Recorder.Record only ever inserts one row and
// never itself calls back into AuditService.List (or anything else),
// so there is no path by which retrieving audit data could trigger an
// unbounded chain of further audit-access events (master prompt's
// explicit "must not recursively generate an unbounded chain" concern,
// satisfied structurally rather than by a runtime guard).
func (s *AuditService) List(ctx context.Context, user auth.AuthenticatedUser, filter AuditListFilter, page utils.Pagination) (*AuditListResult, error) {
	allowed, err := s.authz.HasPermission(ctx, user, authz.ActionAuditRead)
	if err != nil {
		return nil, utils.ErrInternal(err)
	}
	if !allowed {
		return nil, utils.ErrForbidden(genericDocumentForbiddenMessage)
	}

	listArg := generated.ListAuditEntriesFilteredParams{
		UserID:       filter.UserID,
		Role:         filter.Role,
		Action:       filter.Action,
		ResourceType: filter.ResourceType,
		ResourceID:   filter.ResourceID,
		CaseID:       filter.CaseID,
		FromTs:       toTimestamptz(filter.From),
		ToTs:         toTimestamptz(filter.To),
		OffsetVal:    page.Offset(),
		LimitVal:     page.Limit(),
	}
	countArg := generated.CountAuditEntriesFilteredParams{
		UserID: filter.UserID, Role: filter.Role, Action: filter.Action,
		ResourceType: filter.ResourceType, ResourceID: filter.ResourceID, CaseID: filter.CaseID,
		FromTs: toTimestamptz(filter.From), ToTs: toTimestamptz(filter.To),
	}

	ident := repository.AppIdentity{UserID: user.ID, Role: effectiveCaseRole(user)}
	var rows []generated.AuditLog
	var total int64
	err = repository.WithTx(ctx, s.pool, ident, func(ctx context.Context, q *generated.Queries) error {
		repo := repository.NewAuditRepo(q)
		r, err := repo.ListFiltered(ctx, listArg)
		if err != nil {
			return fmt.Errorf("list audit entries: %w", err)
		}
		rows = r

		total, err = repo.CountFiltered(ctx, countArg)
		return err
	})
	if err != nil {
		return nil, utils.ErrInternal(err)
	}

	entries := make([]AuditEntrySummary, len(rows))
	for i, r := range rows {
		entries[i] = toAuditEntrySummary(r)
	}

	role := effectiveCaseRole(user)
	s.recorder.Record(ctx, auditpkg.Event{
		Action:       "AUDIT_ACCESSED",
		ResourceType: "audit_log",
		UserID:       &user.ID,
		Role:         role,
		Metadata: map[string]any{
			"result_count": len(entries),
			"total":        total,
		},
	})

	return &AuditListResult{Entries: entries, Meta: page.BuildMeta(total)}, nil
}

// VerifyChain authorizes user for audit:verify (ADMIN-only per the seed
// data), then walks the ENTIRE hash chain from fromSeq (0 = genesis) in
// bounded-size batches (internal/audit.VerifyBatch, called once per
// batch, never holding more than defaultVerifyBatchSize rows in memory
// at once), stopping at the first integrity failure found or after
// maxEntries entries have been checked in this call (whichever comes
// first) — see maxVerifyEntriesPerRequest's doc comment for why a cap
// exists at all without introducing a background job.
//
// ADMIN's audit_log_select RLS policy already returns every row
// unfiltered (the OR-branch `current_app_role() = 'ADMIN'`) — exactly
// the unrestricted, whole-chain view verification needs, and exactly
// why only ADMIN may call this: verifying "the chain" only makes sense
// against the complete, real sequence, never a per-user/per-case
// filtered subset that could itself be mistaken for the whole chain.
func (s *AuditService) VerifyChain(ctx context.Context, user auth.AuthenticatedUser, fromSeq int64, maxEntries int32) (*ChainVerificationResult, error) {
	allowed, err := s.authz.HasPermission(ctx, user, authz.ActionAuditVerify)
	if err != nil {
		return nil, utils.ErrInternal(err)
	}
	if !allowed {
		return nil, utils.ErrForbidden(genericDocumentForbiddenMessage)
	}

	if maxEntries <= 0 || maxEntries > maxVerifyEntriesPerRequest {
		maxEntries = maxVerifyEntriesPerRequest
	}

	ident := repository.AppIdentity{UserID: user.ID, Role: effectiveCaseRole(user)}

	var totalEntries int64
	var checked int64
	var expectedPrev []byte
	var failure *auditpkg.BatchResult
	seq := fromSeq
	haveMore := true

	err = repository.WithTx(ctx, s.pool, ident, func(ctx context.Context, q *generated.Queries) error {
		repo := repository.NewAuditRepo(q)

		total, err := repo.Count(ctx)
		if err != nil {
			return fmt.Errorf("count audit entries: %w", err)
		}
		totalEntries = total

		// Resuming (fromSeq > 0) means "everything up to and including
		// fromSeq was already confirmed valid by an earlier call" — the
		// entry immediately after it must chain from ITS hash, not from
		// nil (which would wrongly demand a second genesis). Anchor
		// expectedPrev on the entry actually AT fromSeq before scanning
		// forward from there. fromSeq itself is never re-verified here
		// (it already was, in whichever earlier call produced it as
		// NextSeq) — only entries strictly after it are.
		if fromSeq > 0 {
			anchorRows, err := repo.ListFromSeq(ctx, fromSeq-1, 1)
			if err != nil {
				return fmt.Errorf("load anchor entry at seq %d: %w", fromSeq, err)
			}
			if len(anchorRows) == 0 || anchorRows[0].Seq == nil || *anchorRows[0].Seq != fromSeq {
				return utils.ErrBadRequest("from_seq does not correspond to an existing audit entry")
			}
			expectedPrev = anchorRows[0].Hash
		}

		for checked < int64(maxEntries) {
			batchLimit := int32(defaultVerifyBatchSize)
			remaining := int64(maxEntries) - checked
			if remaining < int64(batchLimit) {
				batchLimit = int32(remaining)
			}

			rows, err := repo.ListFromSeq(ctx, seq, batchLimit)
			if err != nil {
				return fmt.Errorf("list audit entries from seq %d: %w", seq, err)
			}
			if len(rows) == 0 {
				haveMore = false
				break
			}

			result := auditpkg.VerifyBatch(rows, expectedPrev)
			checked += int64(result.EntriesChecked)
			if !result.OK {
				failure = &result
				haveMore = false
				break
			}

			expectedPrev = result.LastHash
			lastRow := rows[len(rows)-1]
			if lastRow.Seq != nil {
				seq = *lastRow.Seq
			}
			if len(rows) < int(batchLimit) {
				haveMore = false
				break
			}
		}
		return nil
	})
	if err != nil {
		if appErr, ok := utils.AsAppError(err); ok {
			return nil, appErr
		}
		return nil, utils.ErrInternal(err)
	}

	role := effectiveCaseRole(user)
	result := &ChainVerificationResult{
		EntriesChecked: checked,
		TotalEntries:   totalEntries,
		VerifiedAt:     time.Now().UTC(),
	}
	if haveMore {
		nextSeq := seq
		result.NextSeq = &nextSeq
	}

	if failure != nil {
		result.Status = auditpkg.VerificationStatusIntegrityFailure
		result.FailedEntryID = failure.FailedEntryID
		result.FailedSeq = failure.FailedSeq
		result.Reason = failure.Reason
		result.ExpectedPrevHash = hexOrEmpty(failure.ExpectedPrevHash)
		result.ActualPrevHash = hexOrEmpty(failure.ActualPrevHash)
		result.ExpectedHash = hexOrEmpty(failure.ExpectedHash)
		result.ActualHash = hexOrEmpty(failure.ActualHash)
	} else {
		result.Status = auditpkg.VerificationStatusVerified
	}

	s.recorder.Record(ctx, auditpkg.Event{
		Action:       "AUDIT_CHAIN_VERIFICATION_REQUESTED",
		ResourceType: "audit_log",
		UserID:       &user.ID,
		Role:         role,
		Metadata: map[string]any{
			"status":          result.Status,
			"entries_checked": result.EntriesChecked,
			"from_seq":        fromSeq,
		},
	})

	return result, nil
}

func toAuditEntrySummary(r generated.AuditLog) AuditEntrySummary {
	seq := int64(0)
	if r.Seq != nil {
		seq = *r.Seq
	}
	return AuditEntrySummary{
		ID:           r.ID,
		Seq:          seq,
		Timestamp:    r.Timestamp,
		UserID:       r.UserID,
		Role:         r.Role,
		Action:       r.Action,
		ResourceType: r.ResourceType,
		ResourceID:   r.ResourceID,
		CaseID:       r.CaseID,
		Metadata:     r.Metadata,
		PrevHash:     hexOrEmpty(r.PrevHash),
		Hash:         hex.EncodeToString(r.Hash),
	}
}

func hexOrEmpty(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	return hex.EncodeToString(b)
}
