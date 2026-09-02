package repository

import (
	"context"

	"github.com/google/uuid"

	"evidentia/backend/db/generated"
)

// DocumentRepo wraps the document/redaction queries. There is deliberately
// no Delete method: evidence documents are archived, never deleted (no
// DELETE grant on the runtime role either — see the migration).
type DocumentRepo struct {
	q *generated.Queries
}

func NewDocumentRepo(q *generated.Queries) *DocumentRepo {
	return &DocumentRepo{q: q}
}

func (r *DocumentRepo) Create(ctx context.Context, arg generated.CreateDocumentParams) (generated.Document, error) {
	return r.q.CreateDocument(ctx, arg)
}

func (r *DocumentRepo) GetByID(ctx context.Context, id uuid.UUID) (generated.Document, error) {
	return r.q.GetDocumentByID(ctx, id)
}

func (r *DocumentRepo) ListByCase(ctx context.Context, caseID uuid.UUID, limit, offset int32) ([]generated.Document, error) {
	return r.q.ListDocumentsByCase(ctx, generated.ListDocumentsByCaseParams{CaseID: caseID, Limit: limit, Offset: offset})
}

// ListDerivatives returns documents produced FROM parentID (e.g. redacted
// versions) — see documents.parent_document_id in the migration.
func (r *DocumentRepo) ListDerivatives(ctx context.Context, parentID uuid.UUID) ([]generated.Document, error) {
	return r.q.ListDocumentDerivatives(ctx, &parentID)
}

func (r *DocumentRepo) CountByCase(ctx context.Context, caseID uuid.UUID) (int64, error) {
	return r.q.CountDocumentsByCase(ctx, caseID)
}

func (r *DocumentRepo) UpdateStatus(ctx context.Context, id uuid.UUID, status string) error {
	return r.q.UpdateDocumentStatus(ctx, generated.UpdateDocumentStatusParams{ID: id, Status: status})
}

// ---- Redactions ----

func (r *DocumentRepo) CreateRedaction(ctx context.Context, arg generated.CreateRedactionParams) (generated.Redaction, error) {
	return r.q.CreateRedaction(ctx, arg)
}

func (r *DocumentRepo) GetRedactionByResultDocument(ctx context.Context, resultDocumentID uuid.UUID) (generated.Redaction, error) {
	return r.q.GetRedactionByResultDocument(ctx, resultDocumentID)
}

func (r *DocumentRepo) ListRedactionsBySource(ctx context.Context, sourceDocumentID uuid.UUID) ([]generated.Redaction, error) {
	return r.q.ListRedactionsBySourceDocument(ctx, sourceDocumentID)
}
