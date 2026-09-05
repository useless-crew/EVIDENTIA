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
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"evidentia/backend/db/generated"
	auditpkg "evidentia/backend/internal/audit"
	"evidentia/backend/internal/auth"
	"evidentia/backend/internal/authz"
	"evidentia/backend/internal/jobs"
	"evidentia/backend/internal/models"
	"evidentia/backend/internal/realtime"
	"evidentia/backend/internal/repository"
	"evidentia/backend/internal/utils"
)

// defaultVerifyBatchSize bounds how many rows a single database round
// trip fetches during chain verification — the actual memory-bounding
// mechanism master prompt's "must not require loading millions of audit
// entries into RAM" requires, AND (System 11) the natural throttle for
// how often a running verification writes progress to PostgreSQL /
// publishes an SSE event: once per batch, never once per row.
const defaultVerifyBatchSize = 1000

// System 11's async job-lifecycle constants (in addition to the
// VerificationStatus{Verified,IntegrityFailure} outcomes System 10 already
// defined in internal/audit — see that package for the complete
// vocabulary, kept in one place deliberately).
const (
	// staleQueuedThreshold/staleRunningThreshold: how long a QUEUED/
	// RUNNING verification may go without any progress before it is
	// presumed to belong to a worker that died without ever reaching its
	// own completion/failure handling (process killed, not merely slow) —
	// see reconcileStale's doc comment for why this is checked lazily, at
	// READ time, rather than by a separate sweeper process. QUEUED gets a
	// longer grace period than RUNNING: an idle worker fleet (e.g. none
	// currently deployed) leaving a job QUEUED is a normal, recoverable
	// state for longer than a RUNNING job going silent mid-verification
	// legitimately should.
	staleQueuedThreshold  = 5 * time.Minute
	staleRunningThreshold = 2 * time.Minute
)

// workerIdentityUserID is the transaction-local RLS identity (app.user_id)
// AuditService's background worker methods (RunVerification,
// MarkVerificationOperationallyFailed, and reconcileStale's own queries)
// use for every audit_verifications/audit_log query they run. Every RLS
// policy on audit_verifications checks ONLY current_app_role() = 'ADMIN'
// (see db/migrations/000005_audit_verifications.up.sql) — never
// current_app_user_id() — so, unlike internal/audit.ChainWriter's
// systemActorID (which DOES need to satisfy audit_log_insert's "IS NOT
// NULL" check), this value's only job is being a non-nil placeholder;
// it is never written into any column, never compared against anything,
// and never needs to correspond to a real users row. A dedicated,
// documented sentinel — never audit_log's own systemActorID — keeps the
// two packages' internal conventions independently readable without
// implying a coupling that doesn't exist.
var workerIdentityUserID = uuid.MustParse("00000000-0000-0000-0000-00000000a002")

// workerIdentity is the AppIdentity every worker-side transaction in this
// file uses — see workerIdentityUserID's doc comment for why ADMIN and
// this specific sentinel are both required and sufficient.
var workerIdentity = repository.AppIdentity{UserID: workerIdentityUserID, Role: models.RoleAdmin}

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

// StartVerificationResult is POST /audit/verify-chain's response data
// (System 11 — asynchronous). 202 Accepted: the request to verify has
// been accepted, not that verification has completed — see
// VerificationDetail/GET /audit/verify-chain/:id for the actual outcome.
// If a verification was already QUEUED/RUNNING, this is THAT run's own
// id/status/created_at, never a duplicate second run (see
// AuditService.StartVerification's doc comment). JobID (System 12) is the
// Asynq task ID this verification runs as — deterministically derived
// from ID alone (see jobs.AuditVerifyChainJobID), so it is always
// present, even when this response describes an already-active run this
// call did not itself enqueue.
type StartVerificationResult struct {
	ID        uuid.UUID `json:"verification_id"`
	JobID     string    `json:"job_id"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
}

// VerificationDetail is GET /audit/verify-chain/:id's (and one element of
// GET /audit/verifications') response shape — the complete, safe-to-expose
// state of one verification run. ExactLY the fields an SSE
// realtime.VerificationEvent also carries (see that type's doc comment for
// why the two must never diverge). Every Failed*/FailureType/
// FailureReason field is populated only for INTEGRITY_FAILURE/FAILED (see
// audit_verifications_failure_fields_check) and never carries metadata
// content, SQL text, file paths, or credentials.
type VerificationDetail struct {
	ID                uuid.UUID  `json:"verification_id"`
	JobID             string     `json:"job_id"`
	Status            string     `json:"status"`
	EntriesChecked    int64      `json:"entries_checked"`
	TotalEntries      *int64     `json:"total_entries,omitempty"`
	ProgressPercent   *float64   `json:"progress_percent,omitempty"`
	LastSeqChecked    *int64     `json:"last_seq_checked,omitempty"`
	FailedEntryID     *uuid.UUID `json:"failed_entry_id,omitempty"`
	FailedSeq         *int64     `json:"failed_seq,omitempty"`
	FailureType       string     `json:"failure_type,omitempty"`
	FailureReason     string     `json:"failure_reason,omitempty"`
	RequestedByUserID uuid.UUID  `json:"requested_by_user_id"`
	RequestedByRole   string     `json:"requested_by_role,omitempty"`
	StartedAt         *time.Time `json:"started_at,omitempty"`
	CompletedAt       *time.Time `json:"completed_at,omitempty"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
}

// VerificationListFilter is GET /audit/verifications' optional filter
// set — NULL means "no constraint", the same convention AuditListFilter
// already established.
type VerificationListFilter struct {
	Status      *string
	RequestedBy *uuid.UUID
	From        *time.Time
	To          *time.Time
}

// VerificationListResult is GET /audit/verifications' response data.
type VerificationListResult struct {
	Verifications []VerificationDetail `json:"verifications"`
	Meta          utils.Meta           `json:"meta"`
}

// IntegritySummary is GET /audit/integrity's response data — the
// dashboard's at-a-glance "is the chain intact" view: cheap aggregate
// facts (a single COUNT and the current chain head, both already
// efficient index-backed reads — see CountAuditEntries/
// GetLatestAuditEntry's own doc comments) plus the most recent
// verification run, never a fresh full-chain scan of its own.
type IntegritySummary struct {
	TotalEntries     int64               `json:"total_entries"`
	ChainHeadSeq     *int64              `json:"chain_head_seq,omitempty"`
	ChainHeadHash    string              `json:"chain_head_hash,omitempty"`
	LastVerification *VerificationDetail `json:"last_verification,omitempty"`
}

// AuditService owns audit-trail retrieval and chain verification.
// Deliberately does NOT own audit event RECORDING — every other service
// in this codebase already records its own events directly through the
// shared audit.Recorder (internal/audit.ChainWriter, once wired into
// app.New — see that type's own doc comment for why no existing caller
// needs to change). This type exists for the two read-side operations
// only: GET /audit and POST /audit/verify-chain.
type AuditService struct {
	pool        *pgxpool.Pool
	authz       *authz.Service
	recorder    auditpkg.Recorder
	jobClient   *jobs.Client
	broadcaster *realtime.Broadcaster
	logger      *slog.Logger
}

func NewAuditService(pool *pgxpool.Pool, authzService *authz.Service, recorder auditpkg.Recorder, jobClient *jobs.Client, broadcaster *realtime.Broadcaster, logger *slog.Logger) *AuditService {
	return &AuditService{pool: pool, authz: authzService, recorder: recorder, jobClient: jobClient, broadcaster: broadcaster, logger: logger}
}

// Broadcaster exposes the shared realtime.Broadcaster so
// internal/handlers/audit's SSE route can Subscribe to it directly — the
// handler needs no OTHER access to AuditService's internals for this, and
// the broadcaster itself has no service-layer dependency of its own (see
// internal/realtime's package doc comment).
func (s *AuditService) Broadcaster() *realtime.Broadcaster {
	return s.broadcaster
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

// StartVerification authorizes user for audit:verify (ADMIN-only per the
// seed data — verifying "the chain" only makes sense against the
// complete, unfiltered sequence, exactly like System 10's synchronous
// VerifyChain this replaces already required), then either dispatches a
// NEW background verification job or, if one is already QUEUED/RUNNING,
// returns THAT run's id/status instead of starting a redundant second
// full-chain scan concurrently. The de-duplication is enforced at the
// DATABASE level (idx_audit_verifications_single_active, the exact same
// "unique index on a constant expression" idiom audit_log's own genesis
// uniqueness already uses — see db/migrations/000005_audit_verifications.
// up.sql), not by a check-then-insert race in this method: CreateAuditVerification
// either succeeds (this really is a new job) or fails with a 23505 on
// that specific index (isActiveVerificationConflict below), in which case
// this method reads back and returns the ALREADY-active row instead of
// treating the conflict as an error.
func (s *AuditService) StartVerification(ctx context.Context, user auth.AuthenticatedUser) (*StartVerificationResult, error) {
	allowed, err := s.authz.HasPermission(ctx, user, authz.ActionAuditVerify)
	if err != nil {
		return nil, utils.ErrInternal(err)
	}
	if !allowed {
		return nil, utils.ErrForbidden(genericDocumentForbiddenMessage)
	}

	role := effectiveCaseRole(user)
	ident := repository.AppIdentity{UserID: user.ID, Role: role}

	var row generated.AuditVerification
	var createdNew bool
	createErr := repository.WithTx(ctx, s.pool, ident, func(ctx context.Context, q *generated.Queries) error {
		created, err := repository.NewAuditVerificationRepo(q).Create(ctx, generated.CreateAuditVerificationParams{
			RequestedByUserID: user.ID,
			RequestedByRole:   nilIfEmptyString(role),
		})
		if err != nil {
			return err
		}
		row = created
		createdNew = true
		return nil
	})

	if createErr != nil {
		if !isActiveVerificationConflict(createErr) {
			return nil, utils.ErrInternal(fmt.Errorf("create verification: %w", createErr))
		}
		// PostgreSQL aborts an ENTIRE transaction on any error — a second
		// query (GetActive) cannot run inside the SAME transaction the
		// conflicting Create just failed in (it would fail again with
		// "current transaction is aborted"). The transaction above has
		// already been rolled back (see repository.WithTx's deferred
		// Rollback), so this read runs in a fresh one.
		activeErr := repository.WithTx(ctx, s.pool, ident, func(ctx context.Context, q *generated.Queries) error {
			active, err := repository.NewAuditVerificationRepo(q).GetActive(ctx)
			row = active
			return err
		})
		if activeErr != nil {
			return nil, utils.ErrInternal(fmt.Errorf("load active verification after conflict: %w", activeErr))
		}
	}

	if createdNew {
		if enqueueErr := s.jobClient.EnqueueVerifyAuditChain(ctx, row.ID); enqueueErr != nil {
			s.logger.ErrorContext(ctx, "audit verification: enqueue failed", slog.String("verification_id", row.ID.String()), slog.String("error", enqueueErr.Error()))
			return nil, utils.ErrInternal(enqueueErr)
		}
	}

	s.recorder.Record(ctx, auditpkg.Event{
		Action:       "AUDIT_CHAIN_VERIFICATION_REQUESTED",
		ResourceType: "audit_verification",
		ResourceID:   &row.ID,
		UserID:       &user.ID,
		Role:         role,
		Metadata: map[string]any{
			"deduplicated_to_existing_run": !createdNew,
		},
	})

	return &StartVerificationResult{ID: row.ID, JobID: jobs.AuditVerifyChainJobID(row.ID), Status: row.Status, CreatedAt: row.CreatedAt}, nil
}

// GetVerification authorizes user for audit:verify, then returns one
// verification's current state — reconciling it to FAILED first if it
// looks stale (see reconcileStale) so a caller polling this endpoint (or
// an SSE client's initial snapshot — see internal/handlers/audit's events
// route) never sees a QUEUED/RUNNING status that has actually been dead
// for minutes.
func (s *AuditService) GetVerification(ctx context.Context, user auth.AuthenticatedUser, id uuid.UUID) (*VerificationDetail, error) {
	allowed, err := s.authz.HasPermission(ctx, user, authz.ActionAuditVerify)
	if err != nil {
		return nil, utils.ErrInternal(err)
	}
	if !allowed {
		return nil, utils.ErrForbidden(genericDocumentForbiddenMessage)
	}

	ident := repository.AppIdentity{UserID: user.ID, Role: effectiveCaseRole(user)}
	var row generated.AuditVerification
	err = repository.WithTx(ctx, s.pool, ident, func(ctx context.Context, q *generated.Queries) error {
		vrepo := repository.NewAuditVerificationRepo(q)
		r, err := vrepo.GetByID(ctx, id)
		if err != nil {
			return err
		}
		reconciled, err := s.reconcileStale(ctx, q, r)
		row = reconciled
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, utils.ErrNotFound("Verification not found")
	}
	if err != nil {
		return nil, utils.ErrInternal(err)
	}

	detail := toVerificationDetail(row)
	return &detail, nil
}

// ListVerifications authorizes user for audit:verify, then returns a
// filtered, paginated verification history — the same optional-filter,
// RLS-beneath-it convention System 10's AuditService.List already
// established for audit_log itself (a filter can only narrow, RLS
// narrows independently beneath it — here, RLS narrows to "nothing at
// all" for anyone but ADMIN, which is exactly the point).
func (s *AuditService) ListVerifications(ctx context.Context, user auth.AuthenticatedUser, filter VerificationListFilter, page utils.Pagination) (*VerificationListResult, error) {
	allowed, err := s.authz.HasPermission(ctx, user, authz.ActionAuditVerify)
	if err != nil {
		return nil, utils.ErrInternal(err)
	}
	if !allowed {
		return nil, utils.ErrForbidden(genericDocumentForbiddenMessage)
	}

	listArg := generated.ListAuditVerificationsFilteredParams{
		Status:            filter.Status,
		RequestedByUserID: filter.RequestedBy,
		FromTs:            toTimestamptz(filter.From),
		ToTs:              toTimestamptz(filter.To),
		OffsetVal:         page.Offset(),
		LimitVal:          page.Limit(),
	}
	countArg := generated.CountAuditVerificationsFilteredParams{
		Status: filter.Status, RequestedByUserID: filter.RequestedBy,
		FromTs: toTimestamptz(filter.From), ToTs: toTimestamptz(filter.To),
	}

	ident := repository.AppIdentity{UserID: user.ID, Role: effectiveCaseRole(user)}
	var rows []generated.AuditVerification
	var total int64
	err = repository.WithTx(ctx, s.pool, ident, func(ctx context.Context, q *generated.Queries) error {
		vrepo := repository.NewAuditVerificationRepo(q)
		r, err := vrepo.ListFiltered(ctx, listArg)
		if err != nil {
			return fmt.Errorf("list verifications: %w", err)
		}
		reconciled := make([]generated.AuditVerification, len(r))
		for i, row := range r {
			rr, err := s.reconcileStale(ctx, q, row)
			if err != nil {
				return fmt.Errorf("reconcile verification %s: %w", row.ID, err)
			}
			reconciled[i] = rr
		}
		rows = reconciled

		total, err = vrepo.CountFiltered(ctx, countArg)
		return err
	})
	if err != nil {
		return nil, utils.ErrInternal(err)
	}

	details := make([]VerificationDetail, len(rows))
	for i, r := range rows {
		details[i] = toVerificationDetail(r)
	}
	return &VerificationListResult{Verifications: details, Meta: page.BuildMeta(total)}, nil
}

// GetIntegritySummary authorizes user for audit:verify, then returns the
// dashboard's aggregate view: total_entries (a single, cheap COUNT — see
// CountAuditEntries's own doc comment), the current chain head, and the
// most recent verification run (reconciled if stale). Deliberately does
// NOT run a fresh verification itself — that is what the "Verify Audit
// Chain" button (StartVerification) is for; this endpoint only reports
// what is already known.
func (s *AuditService) GetIntegritySummary(ctx context.Context, user auth.AuthenticatedUser) (*IntegritySummary, error) {
	allowed, err := s.authz.HasPermission(ctx, user, authz.ActionAuditVerify)
	if err != nil {
		return nil, utils.ErrInternal(err)
	}
	if !allowed {
		return nil, utils.ErrForbidden(genericDocumentForbiddenMessage)
	}

	ident := repository.AppIdentity{UserID: user.ID, Role: effectiveCaseRole(user)}
	summary := &IntegritySummary{}
	err = repository.WithTx(ctx, s.pool, ident, func(ctx context.Context, q *generated.Queries) error {
		repo := repository.NewAuditRepo(q)
		total, err := repo.Count(ctx)
		if err != nil {
			return fmt.Errorf("count audit entries: %w", err)
		}
		summary.TotalEntries = total

		latest, err := repo.GetLatest(ctx)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			// Empty chain — no head yet, not an error.
		case err != nil:
			return fmt.Errorf("get chain head: %w", err)
		default:
			summary.ChainHeadSeq = latest.Seq
			summary.ChainHeadHash = hex.EncodeToString(latest.Hash)
		}

		vrepo := repository.NewAuditVerificationRepo(q)
		lastRun, err := vrepo.GetLatest(ctx)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			// No verification has ever been run — a valid, common state.
		case err != nil:
			return fmt.Errorf("get latest verification: %w", err)
		default:
			reconciled, err := s.reconcileStale(ctx, q, lastRun)
			if err != nil {
				return fmt.Errorf("reconcile latest verification: %w", err)
			}
			detail := toVerificationDetail(reconciled)
			summary.LastVerification = &detail
		}
		return nil
	})
	if err != nil {
		return nil, utils.ErrInternal(err)
	}
	return summary, nil
}

// reconcileStale self-heals a QUEUED/RUNNING row whose updated_at is
// older than its threshold (staleQueuedThreshold/staleRunningThreshold)
// into a terminal FAILED state, AND PERSISTS that correction, rather than
// only faking it in the returned value — so the next reader (REST poll,
// SSE initial snapshot, history list) sees the SAME corrected state a
// database-level inspection would, with no divergence between "what this
// call returns" and "what is actually stored". No separate background
// sweeper/cron process exists for this: master prompt's "a failed job
// must not remain RUNNING forever" is satisfied lazily, the first time
// ANYONE looks, which is sufficient for a value that is only ever
// user-observed (there is no other consumer of this table) and keeps
// System 11 from needing a second scheduled process alongside the HTTP
// server and the Asynq worker it already has. Safe to call speculatively
// even if the real worker is about to (or just did) complete the row
// itself: MarkAuditVerificationStale's own `WHERE status IN (...)` guard
// makes the UPDATE a no-op (pgx.ErrNoRows) in that race, and this method
// simply re-reads the now-genuinely-terminal row instead.
func (s *AuditService) reconcileStale(ctx context.Context, q *generated.Queries, row generated.AuditVerification) (generated.AuditVerification, error) {
	if row.Status != auditpkg.VerificationStatusQueued && row.Status != auditpkg.VerificationStatusRunning {
		return row, nil
	}
	threshold := staleRunningThreshold
	if row.Status == auditpkg.VerificationStatusQueued {
		threshold = staleQueuedThreshold
	}
	if time.Since(row.UpdatedAt) < threshold {
		return row, nil
	}

	staleReason := "no progress was recorded for longer than expected — the verification worker is presumed to have stopped unexpectedly"
	vrepo := repository.NewAuditVerificationRepo(q)
	updated, err := vrepo.MarkStale(ctx, generated.MarkAuditVerificationStaleParams{
		ID:            row.ID,
		FailureReason: &staleReason,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return vrepo.GetByID(ctx, row.ID)
	}
	if err != nil {
		return row, fmt.Errorf("mark verification stale: %w", err)
	}
	return updated, nil
}

// RunVerification implements jobs.AuditVerifier — the ENTIRE body of
// System 11's background task, called by jobs.AuditVerificationHandler.
// ProcessTask. It reuses System 10's exact verification primitives
// (auditpkg.VerifyBatch/ComputeEntryHash via ListFromSeq's identical
// keyset-paginated traversal AuditRepo already provides) — nothing here
// reimplements hashing, canonicalization, or chain traversal; see this
// file's own package doc comment and docs/AUDIT_CHAIN.md for the single
// authoritative implementation this calls into.
//
// An error return means the run could NOT complete — an operational
// failure Asynq should retry (see jobs.NewAuditVerificationErrorHandler).
// VERIFIED and INTEGRITY_FAILURE are both reached via a nil return: the
// job RAN TO COMPLETION either way, exactly like System 10's synchronous
// VerifyChain treated both as a 200, never an error.
func (s *AuditService) RunVerification(ctx context.Context, verificationID uuid.UUID) error {
	var total int64
	if err := repository.WithTx(ctx, s.pool, workerIdentity, func(ctx context.Context, q *generated.Queries) error {
		t, err := repository.NewAuditRepo(q).Count(ctx)
		total = t
		return err
	}); err != nil {
		return fmt.Errorf("count audit entries: %w", err)
	}

	var verification generated.AuditVerification
	err := repository.WithTx(ctx, s.pool, workerIdentity, func(ctx context.Context, q *generated.Queries) error {
		r, err := repository.NewAuditVerificationRepo(q).MarkRunning(ctx, generated.MarkAuditVerificationRunningParams{
			ID: verificationID, TotalEntries: &total,
		})
		verification = r
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		// Already RUNNING/terminal (e.g. a redelivered/duplicate task
		// attempt) — nothing to redo; not an operational failure.
		return nil
	}
	if err != nil {
		return fmt.Errorf("mark verification running: %w", err)
	}

	s.broadcaster.Publish(realtime.VerificationEvent{
		Type: realtime.EventVerificationStarted, VerificationID: verificationID,
		Status: auditpkg.VerificationStatusRunning, TotalEntries: &total, Timestamp: time.Now().UTC(),
	})

	checked, seq, failure, err := s.verifyBatches(ctx, verificationID, total)
	if err != nil {
		return fmt.Errorf("verify batches: %w", err)
	}

	final, err := s.completeVerification(ctx, verificationID, checked, failure)
	if err != nil {
		return fmt.Errorf("persist verification result: %w", err)
	}
	_ = seq

	s.recorder.Record(ctx, auditpkg.Event{
		Action:       "AUDIT_CHAIN_VERIFICATION_COMPLETED",
		ResourceType: "audit_verification",
		ResourceID:   &verificationID,
		UserID:       &verification.RequestedByUserID,
		Role:         models.RoleAdmin,
		Metadata: map[string]any{
			"status":          final.Status,
			"entries_checked": final.EntriesChecked,
		},
	})

	return nil
}

// verifyBatches walks the chain from genesis in defaultVerifyBatchSize
// pages, persisting + broadcasting progress once per batch (never per
// row — see defaultVerifyBatchSize's own doc comment), and returns the
// total entries checked, the last seq examined, and the first failure
// found (nil on a clean VERIFIED run). Each batch is its own short
// transaction — this function never holds one open across the whole
// run, so a large chain's verification never holds locks or a connection
// for its entire (potentially long) duration.
func (s *AuditService) verifyBatches(ctx context.Context, verificationID uuid.UUID, total int64) (checked int64, lastSeq int64, failure *auditpkg.BatchResult, err error) {
	var expectedPrev []byte
	seq := int64(0)

	for {
		select {
		case <-ctx.Done():
			return checked, seq, nil, fmt.Errorf("verification cancelled: %w", ctx.Err())
		default:
		}

		var rows []generated.AuditLog
		txErr := repository.WithTx(ctx, s.pool, workerIdentity, func(ctx context.Context, q *generated.Queries) error {
			r, err := repository.NewAuditRepo(q).ListFromSeq(ctx, seq, int32(defaultVerifyBatchSize))
			rows = r
			return err
		})
		if txErr != nil {
			return checked, seq, nil, fmt.Errorf("list audit entries from seq %d: %w", seq, txErr)
		}
		if len(rows) == 0 {
			return checked, seq, nil, nil
		}

		result := auditpkg.VerifyBatch(rows, expectedPrev)
		checked += int64(result.EntriesChecked)

		if result.OK {
			if lastRow := rows[len(rows)-1]; lastRow.Seq != nil {
				seq = *lastRow.Seq
			}
		} else if result.FailedSeq != nil {
			seq = *result.FailedSeq
		}

		if progressErr := repository.WithTx(ctx, s.pool, workerIdentity, func(ctx context.Context, q *generated.Queries) error {
			return repository.NewAuditVerificationRepo(q).UpdateProgress(ctx, generated.UpdateAuditVerificationProgressParams{
				ID: verificationID, EntriesChecked: checked, LastSeqChecked: &seq,
			})
		}); progressErr != nil {
			return checked, seq, nil, fmt.Errorf("persist progress: %w", progressErr)
		}

		s.broadcaster.Publish(s.progressEvent(verificationID, checked, total))

		if !result.OK {
			return checked, seq, &result, nil
		}
		expectedPrev = result.LastHash
		if len(rows) < defaultVerifyBatchSize {
			return checked, seq, nil, nil
		}
	}
}

func (s *AuditService) progressEvent(verificationID uuid.UUID, checked, total int64) realtime.VerificationEvent {
	event := realtime.VerificationEvent{
		Type: realtime.EventVerificationProgress, VerificationID: verificationID,
		Status: auditpkg.VerificationStatusRunning, EntriesChecked: checked, Timestamp: time.Now().UTC(),
	}
	if total > 0 {
		event.TotalEntries = &total
		pct := float64(checked) / float64(total) * 100
		event.ProgressPct = &pct
	}
	return event
}

// completeVerification persists the terminal VERIFIED/INTEGRITY_FAILURE
// state and publishes the matching completion SSE event.
func (s *AuditService) completeVerification(ctx context.Context, verificationID uuid.UUID, checked int64, failure *auditpkg.BatchResult) (generated.AuditVerification, error) {
	status := auditpkg.VerificationStatusVerified
	var failedEntryID *uuid.UUID
	var failedSeq *int64
	var failureType, reason *string
	if failure != nil {
		status = auditpkg.VerificationStatusIntegrityFailure
		failedEntryID = failure.FailedEntryID
		failedSeq = failure.FailedSeq
		failureType = nilIfEmptyString(failure.FailureType)
		reason = nilIfEmptyString(failure.Reason)
	}

	var completed generated.AuditVerification
	err := repository.WithTx(ctx, s.pool, workerIdentity, func(ctx context.Context, q *generated.Queries) error {
		r, err := repository.NewAuditVerificationRepo(q).Complete(ctx, generated.CompleteAuditVerificationParams{
			ID: verificationID, Status: status, EntriesChecked: checked,
			FailedEntryID: failedEntryID, FailedSeq: failedSeq,
			FailureType: failureType, FailureReason: reason,
		})
		completed = r
		return err
	})
	if err != nil {
		return completed, err
	}

	event := realtime.VerificationEvent{
		VerificationID: verificationID, Status: status, EntriesChecked: checked, Timestamp: time.Now().UTC(),
	}
	if failure != nil {
		event.Type = realtime.EventVerificationIntegrityFailure
		event.FailedEntryID = failedEntryID
		event.FailureType = failure.FailureType
		event.FailureReason = failure.Reason
	} else {
		event.Type = realtime.EventVerificationCompleted
	}
	s.broadcaster.Publish(event)

	return completed, nil
}

// MarkVerificationOperationallyFailed implements jobs.AuditFailureRecorder —
// called by jobs.NewAuditVerificationErrorHandler ONLY once Asynq has
// exhausted every retry attempt for a task (never on an intermediate
// attempt — see that function's own doc comment), so a transient error
// that succeeds on a later retry never leaves a stray FAILED row
// alongside the eventual real VERIFIED/INTEGRITY_FAILURE result. cause is
// logged operationally by the caller already; this method persists only a
// safe, generic, classified reason — never cause's own raw text (which
// may carry driver/SQL detail) into the database or API response.
func (s *AuditService) MarkVerificationOperationallyFailed(ctx context.Context, verificationID uuid.UUID, cause error) error {
	failureType := auditpkg.OperationalFailureDatabaseError
	reason := "verification could not complete due to an operational error"
	if errors.Is(cause, context.DeadlineExceeded) {
		failureType = auditpkg.OperationalFailureTimeout
		reason = "verification did not complete within the allotted time"
	}

	return repository.WithTx(ctx, s.pool, workerIdentity, func(ctx context.Context, q *generated.Queries) error {
		vrepo := repository.NewAuditVerificationRepo(q)
		row, err := vrepo.GetByID(ctx, verificationID)
		if err != nil {
			return err
		}
		if row.Status != auditpkg.VerificationStatusQueued && row.Status != auditpkg.VerificationStatusRunning {
			return nil // already terminal (raced with a real completion) — nothing to do
		}
		_, err = vrepo.Complete(ctx, generated.CompleteAuditVerificationParams{
			ID: verificationID, Status: auditpkg.VerificationStatusFailed, EntriesChecked: row.EntriesChecked,
			FailureType: &failureType, FailureReason: &reason,
		})
		if err != nil {
			return err
		}
		s.broadcaster.Publish(realtime.VerificationEvent{
			Type: realtime.EventVerificationFailed, VerificationID: verificationID,
			Status: auditpkg.VerificationStatusFailed, EntriesChecked: row.EntriesChecked,
			FailureType: failureType, FailureReason: reason, Timestamp: time.Now().UTC(),
		})
		return nil
	})
}

func toVerificationDetail(r generated.AuditVerification) VerificationDetail {
	d := VerificationDetail{
		ID: r.ID, JobID: jobs.AuditVerifyChainJobID(r.ID), Status: r.Status, EntriesChecked: r.EntriesChecked,
		TotalEntries: r.TotalEntries, LastSeqChecked: r.LastSeqChecked,
		FailedEntryID: r.FailedEntryID, FailedSeq: r.FailedSeq,
		FailureType: stringOrEmpty(r.FailureType), FailureReason: stringOrEmpty(r.FailureReason),
		RequestedByUserID: r.RequestedByUserID, RequestedByRole: stringOrEmpty(r.RequestedByRole),
		StartedAt: timestamptzPtr(r.StartedAt), CompletedAt: timestamptzPtr(r.CompletedAt),
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
	if r.TotalEntries != nil && *r.TotalEntries > 0 {
		pct := float64(r.EntriesChecked) / float64(*r.TotalEntries) * 100
		if pct > 100 {
			pct = 100
		}
		d.ProgressPercent = &pct
	}
	return d
}

func stringOrEmpty(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func nilIfEmptyString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// isActiveVerificationConflict reports whether err is the unique-violation
// (SQLSTATE 23505) on idx_audit_verifications_single_active — the signal
// that a verification is already QUEUED/RUNNING, which
// StartVerification treats as "return the existing run" rather than an
// error.
func isActiveVerificationConflict(err error) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return false
	}
	return pgErr.Code == "23505" && pgErr.ConstraintName == "idx_audit_verifications_single_active"
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
