package authz

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"evidentia/backend/db/generated"
	"evidentia/backend/internal/auth"
	"evidentia/backend/internal/repository"
)

// CanAccessDocument evaluates whether user may perform action on the
// document identified by documentID. A document carries no independent
// access grant of its own — access is inherited from EITHER of two
// paths: the user's relationship to the document's case (master prompt
// §9: a LAWYER attached to Case A does not thereby gain access to "every
// document in the database", only documents belonging to a case they
// actually have a relationship with), OR an explicit, document-scoped
// delegation via document_shares (see shareGrantsAccess below) — never
// both required, never a third path.
//
// The document's own metadata is loaded — never its bytes; authorization
// must never require reading a file out of MinIO (master prompt §24) —
// under the caller's own RLS identity, so a document belonging to an
// unrelated case is denied identically to a nonexistent document ID
// (same anti-enumeration posture as CanAccessCase).
func (s *Service) CanAccessDocument(ctx context.Context, user auth.AuthenticatedUser, documentID uuid.UUID, action Action) (Decision, error) {
	allowed, err := s.HasPermission(ctx, user, action)
	if err != nil {
		return Decision{}, err
	}
	if !allowed {
		s.recordDenied(ctx, user, action, "document", &documentID, nil, "rbac_permission_denied")
		return deny("permission_denied"), nil
	}

	ident := repository.AppIdentity{UserID: user.ID, Role: effectiveRole(user)}
	var found bool
	var caseID uuid.UUID
	err = repository.WithTx(ctx, s.pool, ident, func(ctx context.Context, q *generated.Queries) error {
		d, err := q.GetDocumentByID(ctx, documentID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil
			}
			return err
		}
		found = true
		caseID = d.CaseID
		return nil
	})
	if err != nil {
		return Decision{}, err
	}
	if !found {
		s.recordDenied(ctx, user, action, "document", &documentID, nil, "not_found_or_no_relationship")
		return deny("not_found_or_no_relationship"), nil
	}

	if isAdmin(user) {
		return allow("admin"), nil
	}

	// Independently re-derive the case relationship rather than trusting
	// that GetDocumentByID succeeding already proves it (master prompt
	// §12: application ABAC is not a replacement for re-checking, even
	// though today RLS's documents_select policy already implies this).
	rel, err := s.loadCaseRelationship(ctx, user, caseID)
	if err != nil {
		return Decision{}, err
	}
	if rel.found && (rel.isOwner || rel.isMember) {
		return allow("case_relationship_verified"), nil
	}

	// Second, narrower authorization path: a case-unrelated user may
	// still hold a valid, ACTIVE, unexpired document_shares grant naming
	// them as recipient for THIS EXACT document — master prompt §19's
	// "user is directly authorized OR user has active valid delegated
	// access". See share_policy.go's shareGrantsAccess for exactly which
	// actions a share can ever cover (never redact/reshare/certificate-
	// create — those never reach this branch regardless of what any
	// share row says).
	delegated, err := s.shareGrantsAccess(ctx, user, documentID, action)
	if err != nil {
		return Decision{}, err
	}
	if delegated {
		return allow("delegated_share"), nil
	}

	s.recordDenied(ctx, user, action, "document", &documentID, &caseID, "not_case_member")
	return deny("not_case_member"), nil
}
