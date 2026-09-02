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

func (r *CertificateRepo) ListByDocument(ctx context.Context, documentID uuid.UUID) ([]generated.ComplianceCertificate, error) {
	return r.q.ListCertificatesByDocument(ctx, documentID)
}
