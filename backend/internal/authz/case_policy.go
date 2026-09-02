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

// caseRelationship is the ABAC-relevant relationship between a user and a
// single case, loaded fresh for every decision — never cached across
// requests or stored on the Service (master prompt §32).
type caseRelationship struct {
	found    bool
	isOwner  bool
	isMember bool
}

// loadCaseRelationship queries the case, and the caller's own active
// case_members row, UNDER THE CALLER'S OWN RLS IDENTITY (repository.WithTx
// with their real user ID/role) — not a privileged bypass. A case
// genuinely invisible to this user under RLS therefore also comes back
// "!found" here, so the two layers reinforce each other instead of one
// silently trusting the other (master prompt §12: "both layers must work
// together"). isOwner/isMember are still computed explicitly from the
// returned rows rather than inferred from "the query succeeded", so a
// future RLS policy defect could not, by itself, turn into a wrong ABAC
// decision.
func (s *Service) loadCaseRelationship(ctx context.Context, user auth.AuthenticatedUser, caseID uuid.UUID) (caseRelationship, error) {
	var rel caseRelationship
	ident := repository.AppIdentity{UserID: user.ID, Role: effectiveRole(user)}

	err := repository.WithTx(ctx, s.pool, ident, func(ctx context.Context, q *generated.Queries) error {
		c, err := q.GetCaseByID(ctx, caseID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil
			}
			return err
		}
		rel.found = true
		rel.isOwner = c.CreatedBy == user.ID

		_, err = q.GetActiveCaseMembership(ctx, generated.GetActiveCaseMembershipParams{CaseID: caseID, UserID: user.ID})
		switch {
		case err == nil:
			rel.isMember = true
		case errors.Is(err, pgx.ErrNoRows):
			// Not a member — fine, isOwner alone may still be sufficient.
		default:
			return err
		}
		return nil
	})
	return rel, err
}

// CanAccessCase evaluates whether user may perform action on the case
// identified by caseID: an RBAC check first (cheap, no resource lookup —
// master prompt §17: "do not perform expensive resource lookup
// unnecessarily for requests that should already fail at RBAC level"),
// then, only if that passes, an ABAC check of the user's actual
// relationship to THIS specific case (ADMIN, the case's creator, or an
// active case_members row — master prompt §8's case-based ABAC).
//
// A case invisible to the user (a guessed/unrelated UUID, or a real case
// they have no relationship to) is denied IDENTICALLY to a case that does
// not exist at all — the returned Decision.Reason differs only for
// server-side diagnostics, never for what a client sees (master prompt
// §21/§25: never confirm resource existence to a caller with no
// relationship to it — the standard IDOR-prevention posture).
func (s *Service) CanAccessCase(ctx context.Context, user auth.AuthenticatedUser, caseID uuid.UUID, action Action) (Decision, error) {
	allowed, err := s.HasPermission(ctx, user, action)
	if err != nil {
		return Decision{}, err
	}
	if !allowed {
		s.recordDenied(ctx, user, action, "case", &caseID, nil, "rbac_permission_denied")
		return deny("permission_denied"), nil
	}

	if isAdmin(user) {
		return allow("admin"), nil
	}

	rel, err := s.loadCaseRelationship(ctx, user, caseID)
	if err != nil {
		return Decision{}, err
	}
	if !rel.found {
		s.recordDenied(ctx, user, action, "case", &caseID, nil, "not_found_or_no_relationship")
		return deny("not_found_or_no_relationship"), nil
	}
	if !rel.isOwner && !rel.isMember {
		s.recordDenied(ctx, user, action, "case", &caseID, &caseID, "not_case_member")
		return deny("not_case_member"), nil
	}

	return allow("case_relationship_verified"), nil
}
