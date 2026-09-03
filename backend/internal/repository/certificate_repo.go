package repository

import (
	"context"

	"github.com/google/uuid"

	"evidentia/backend/db/generated"
)

// CertificateRepo wraps the compliance-certificate queries. Immutable
// once created: no update/delete method, and the runtime role holds no
// UPDATE/DELETE grant on this table either (see the migration).
type CertificateRepo struct {
	q *generated.Queries
}

func NewCertificateRepo(q *generated.Queries) *CertificateRepo {
	return &CertificateRepo{q: q}
}

func (r *CertificateRepo) Create(ctx context.Context, arg generated.CreateCertificateParams) (generated.ComplianceCertificate, error) {
	return r.q.CreateCertificate(ctx, arg)
}

func (r *CertificateRepo) GetByID(ctx context.Context, id uuid.UUID) (generated.ComplianceCertificate, error) {
	return r.q.GetCertificateByID(ctx, id)
}

// GetByDocumentID returns the single certificate bound to documentID, if
// any exists — see GetCertificateByDocumentID's own comment on why at
// most one row can ever match today.
func (r *CertificateRepo) GetByDocumentID(ctx context.Context, documentID uuid.UUID) (generated.ComplianceCertificate, error) {
	return r.q.GetCertificateByDocumentID(ctx, documentID)
}

// GetByDocumentAndHash resolves the "already exists" case after a Create
// call's ON CONFLICT DO NOTHING matched an existing row (see Create).
func (r *CertificateRepo) GetByDocumentAndHash(ctx context.Context, documentID uuid.UUID, documentHash []byte) (generated.ComplianceCertificate, error) {
	return r.q.GetCertificateByDocumentAndHash(ctx, generated.GetCertificateByDocumentAndHashParams{
		DocumentID:   documentID,
		DocumentHash: documentHash,
	})
}

func (r *CertificateRepo) ListByDocument(ctx context.Context, documentID uuid.UUID) ([]generated.ComplianceCertificate, error) {
	return r.q.ListCertificatesByDocument(ctx, documentID)
}
