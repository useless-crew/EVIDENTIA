package repository

import (
	"context"

	"github.com/google/uuid"

	"evidentia/backend/db/generated"
)

// AuditVerificationRepo wraps the audit_verifications queries (System 11).
// Unlike AuditRepo (audit_log — SELECT/INSERT only, never mutated), this
// table IS mutated by design: it tracks a background job's own lifecycle,
// never the audit chain itself. See db/migrations/000005_audit_
// verifications.up.sql for the full RLS/grant model.
type AuditVerificationRepo struct {
	q *generated.Queries
}

func NewAuditVerificationRepo(q *generated.Queries) *AuditVerificationRepo {
	return &AuditVerificationRepo{q: q}
}

func (r *AuditVerificationRepo) Create(ctx context.Context, arg generated.CreateAuditVerificationParams) (generated.AuditVerification, error) {
	return r.q.CreateAuditVerification(ctx, arg)
}

func (r *AuditVerificationRepo) GetActive(ctx context.Context) (generated.AuditVerification, error) {
	return r.q.GetActiveAuditVerification(ctx)
}

func (r *AuditVerificationRepo) GetByID(ctx context.Context, id uuid.UUID) (generated.AuditVerification, error) {
	return r.q.GetAuditVerificationByID(ctx, id)
}

func (r *AuditVerificationRepo) MarkRunning(ctx context.Context, arg generated.MarkAuditVerificationRunningParams) (generated.AuditVerification, error) {
	return r.q.MarkAuditVerificationRunning(ctx, arg)
}

func (r *AuditVerificationRepo) UpdateProgress(ctx context.Context, arg generated.UpdateAuditVerificationProgressParams) error {
	return r.q.UpdateAuditVerificationProgress(ctx, arg)
}

func (r *AuditVerificationRepo) Complete(ctx context.Context, arg generated.CompleteAuditVerificationParams) (generated.AuditVerification, error) {
	return r.q.CompleteAuditVerification(ctx, arg)
}

func (r *AuditVerificationRepo) MarkStale(ctx context.Context, arg generated.MarkAuditVerificationStaleParams) (generated.AuditVerification, error) {
	return r.q.MarkAuditVerificationStale(ctx, arg)
}

func (r *AuditVerificationRepo) ListFiltered(ctx context.Context, arg generated.ListAuditVerificationsFilteredParams) ([]generated.AuditVerification, error) {
	return r.q.ListAuditVerificationsFiltered(ctx, arg)
}

func (r *AuditVerificationRepo) CountFiltered(ctx context.Context, arg generated.CountAuditVerificationsFilteredParams) (int64, error) {
	return r.q.CountAuditVerificationsFiltered(ctx, arg)
}

func (r *AuditVerificationRepo) GetLatest(ctx context.Context) (generated.AuditVerification, error) {
	return r.q.GetLatestAuditVerification(ctx)
}
