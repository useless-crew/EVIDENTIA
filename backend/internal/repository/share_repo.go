package repository

import (
	"context"

	"github.com/google/uuid"

	"evidentia/backend/db/generated"
)

// ShareRepo wraps the document-sharing queries. There is deliberately no
// Delete/Update-arbitrary-field method: a share is immutable history
// once created — the only mutation is Revoke's single ACTIVE -> REVOKED
// transition (no UPDATE grant beyond that exists on the runtime role
// either — see the migration).
type ShareRepo struct {
	q *generated.Queries
}

func NewShareRepo(q *generated.Queries) *ShareRepo {
	return &ShareRepo{q: q}
}

func (r *ShareRepo) Create(ctx context.Context, arg generated.CreateDocumentShareParams) (generated.DocumentShare, error) {
	return r.q.CreateDocumentShare(ctx, arg)
}

// GetByID looks up a share scoped to BOTH its own id and the document it
// belongs to — see the underlying query's doc comment for why this
// double-scoping is the IDOR-relevant part.
func (r *ShareRepo) GetByID(ctx context.Context, shareID, documentID uuid.UUID) (generated.DocumentShare, error) {
	return r.q.GetDocumentShareByID(ctx, generated.GetDocumentShareByIDParams{ID: shareID, DocumentID: documentID})
}

func (r *ShareRepo) ListForDocument(ctx context.Context, documentID uuid.UUID) ([]generated.DocumentShare, error) {
	return r.q.ListDocumentSharesForDocument(ctx, documentID)
}

func (r *ShareRepo) Revoke(ctx context.Context, shareID, documentID uuid.UUID, revokedBy uuid.UUID) (generated.DocumentShare, error) {
	return r.q.RevokeDocumentShare(ctx, generated.RevokeDocumentShareParams{
		ID:              shareID,
		DocumentID:      documentID,
		RevokedByUserID: &revokedBy,
	})
}

// GetActiveForDocumentAndUser is the authorization hot path — see the
// underlying query's doc comment.
func (r *ShareRepo) GetActiveForDocumentAndUser(ctx context.Context, documentID, userID uuid.UUID) (generated.DocumentShare, error) {
	return r.q.GetActiveShareForDocumentAndUser(ctx, generated.GetActiveShareForDocumentAndUserParams{
		DocumentID:       documentID,
		SharedWithUserID: userID,
	})
}

func (r *ShareRepo) ListSharedWithMe(ctx context.Context, userID uuid.UUID, limit, offset int32) ([]generated.ListSharedWithMeRow, error) {
	return r.q.ListSharedWithMe(ctx, generated.ListSharedWithMeParams{SharedWithUserID: userID, Limit: limit, Offset: offset})
}

func (r *ShareRepo) CountSharedWithMe(ctx context.Context, userID uuid.UUID) (int64, error) {
	return r.q.CountSharedWithMe(ctx, userID)
}
