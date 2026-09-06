package repository

import (
	"context"
	"time"

	"github.com/google/uuid"

	"evidentia/backend/db/generated"
)

// AuditRepo wraps the audit_log queries. There is deliberately no
// Update/Delete method — the runtime role holds SELECT + INSERT only (see
// the migration and backend/tests/audit_privileges_test.go). Hash-chain
// computation (deriving hash from prev_hash + entry content) is System 8's
// responsibility; Insert here just stores whatever the caller computed.
type AuditRepo struct {
	q *generated.Queries
}

func NewAuditRepo(q *generated.Queries) *AuditRepo {
	return &AuditRepo{q: q}
}

func (r *AuditRepo) Insert(ctx context.Context, arg generated.InsertAuditEntryParams) (generated.AuditLog, error) {
	return r.q.InsertAuditEntry(ctx, arg)
}

// GetLatest returns the current chain head (highest seq), which System
// 8's writer reads to learn the prev_hash for its next entry.
func (r *AuditRepo) GetLatest(ctx context.Context) (generated.AuditLog, error) {
	return r.q.GetLatestAuditEntry(ctx)
}

func (r *AuditRepo) GetByID(ctx context.Context, id uuid.UUID) (generated.AuditLog, error) {
	return r.q.GetAuditEntryByID(ctx, id)
}

// ListFromSeq walks the chain in order starting just after fromSeq (0 for
// the genesis entry) — for chain verification (System 8).
func (r *AuditRepo) ListFromSeq(ctx context.Context, fromSeq int64, limit int32) ([]generated.AuditLog, error) {
	return r.q.ListAuditEntriesFromSeq(ctx, generated.ListAuditEntriesFromSeqParams{Seq: &fromSeq, Limit: limit})
}

func (r *AuditRepo) ListByCase(ctx context.Context, caseID uuid.UUID, limit, offset int32) ([]generated.AuditLog, error) {
	return r.q.ListAuditEntriesByCase(ctx, generated.ListAuditEntriesByCaseParams{CaseID: &caseID, Limit: limit, Offset: offset})
}

func (r *AuditRepo) ListByUser(ctx context.Context, userID uuid.UUID, limit, offset int32) ([]generated.AuditLog, error) {
	return r.q.ListAuditEntriesByUser(ctx, generated.ListAuditEntriesByUserParams{UserID: &userID, Limit: limit, Offset: offset})
}

func (r *AuditRepo) ListByAction(ctx context.Context, action string, limit, offset int32) ([]generated.AuditLog, error) {
	return r.q.ListAuditEntriesByAction(ctx, generated.ListAuditEntriesByActionParams{Action: action, Limit: limit, Offset: offset})
}

func (r *AuditRepo) ListByDateRange(ctx context.Context, from, to time.Time, limit, offset int32) ([]generated.AuditLog, error) {
	return r.q.ListAuditEntriesByDateRange(ctx, generated.ListAuditEntriesByDateRangeParams{
		Timestamp:   from,
		Timestamp_2: to,
		Limit:       limit,
		Offset:      offset,
	})
}

func (r *AuditRepo) Count(ctx context.Context) (int64, error) {
	return r.q.CountAuditEntries(ctx)
}

// AcquireChainLock takes the transaction-scoped advisory lock
// internal/audit.ChainWriter uses to serialize concurrent chain-head
// reads/writes — see the underlying query's doc comment. Must be called
// before GetLatest within the same transaction.
func (r *AuditRepo) AcquireChainLock(ctx context.Context, lockKey int64) error {
	return r.q.AcquireAuditChainLock(ctx, lockKey)
}

func (r *AuditRepo) ListFiltered(ctx context.Context, arg generated.ListAuditEntriesFilteredParams) ([]generated.AuditLog, error) {
	return r.q.ListAuditEntriesFiltered(ctx, arg)
}

func (r *AuditRepo) CountFiltered(ctx context.Context, arg generated.CountAuditEntriesFilteredParams) (int64, error) {
	return r.q.CountAuditEntriesFiltered(ctx, arg)
}
