package authz

import (
	"context"

	"github.com/google/uuid"

	"evidentia/backend/internal/auth"
)

// CanModifyUserRole gates any operation that assigns or removes a role
// for targetUserID. Two independent checks: the normal RBAC permission
// (per backend/db/seed/001_reference_data.sql, only ADMIN is granted
// user:role today), AND an explicit self-modification block — master
// prompt §26/§27 requires that NO caller, including an admin acting on
// their own account through this exact operation, can grant themselves a
// role through this path. This is deliberately redundant with "no
// non-admin role holds user:role" today: if that grant is ever widened,
// self-escalation stays blocked without depending on this function being
// kept in sync with the seed data.
func (s *Service) CanModifyUserRole(ctx context.Context, actor auth.AuthenticatedUser, targetUserID uuid.UUID) (Decision, error) {
	allowed, err := s.HasPermission(ctx, actor, ActionUserRole)
	if err != nil {
		return Decision{}, err
	}
	if !allowed {
		s.recordDenied(ctx, actor, ActionUserRole, "user", &targetUserID, nil, "rbac_permission_denied")
		return deny("permission_denied"), nil
	}

	if actor.ID == targetUserID {
		s.recordDenied(ctx, actor, ActionUserRole, "user", &targetUserID, nil, "self_role_modification_forbidden")
		return deny("self_role_modification_forbidden"), nil
	}

	return allow("authorized"), nil
}
