package authz

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"evidentia/backend/db/generated"
	"evidentia/backend/internal/audit"
	"evidentia/backend/internal/auth"
	"evidentia/backend/internal/repository"
)

// Service is Evidentia's central authorization engine — the single place
// RBAC (HasPermission) and ABAC (CanAccessCase/CanAccessDocument/
// CanModifyUserRole) decisions are made, so a handler or a future
// background job never hand-rolls its own role/relationship check (master
// prompt §19/§23). It holds no mutable state of its own beyond its
// dependencies (pool, recorder) — every decision is computed fresh from
// the request-scoped auth.AuthenticatedUser passed in, so it is safe for
// concurrent use by construction, not by convention (master prompt §32:
// "never store current authenticated user in a package-level variable").
type Service struct {
	pool     *pgxpool.Pool
	recorder audit.Recorder
}

// NewService builds a Service. recorder is the same internal/audit.Recorder
// interface System 3 depends on (audit.SlogRecorder today; a durable
// hash-chained implementation is System 8's job, per that package's own
// doc comment) — authorization denials are recorded through it exactly
// like authentication failures already are, so swapping in System 8's
// writer later requires no change here either.
func NewService(pool *pgxpool.Pool, recorder audit.Recorder) *Service {
	return &Service{pool: pool, recorder: recorder}
}

// loadPermissions returns the UNION of every permission granted to any
// role in roles — the correct evaluation for a multi-role user (master
// prompt §15/§31). roles/permissions/role_permissions carry no RLS (see
// the migration's own rationale: "no per-row ownership rule for this
// reference/identity data"), so this deliberately runs with no
// application identity (repository.AppIdentity{}) — identical to how
// AuthService already loads roles for login/refresh/ResolveIdentity. An
// unrecognized role name contributes nothing rather than erroring: a role
// that no longer exists in the catalog should fail closed for itself,
// without aborting evaluation of the user's other, still-valid roles.
func (s *Service) loadPermissions(ctx context.Context, roles []string) (PermissionSet, error) {
	perms := make(PermissionSet)
	if len(roles) == 0 {
		return perms, nil
	}

	err := repository.WithTx(ctx, s.pool, repository.AppIdentity{}, func(ctx context.Context, q *generated.Queries) error {
		for _, roleName := range roles {
			role, err := q.GetRoleByName(ctx, roleName)
			if err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					continue
				}
				return err
			}

			rolePerms, err := q.ListPermissionsForRole(ctx, role.ID)
			if err != nil {
				return err
			}
			for _, p := range rolePerms {
				perms.add(Action(p.Name))
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return perms, nil
}

// HasPermission is the RBAC check: does ANY of user's roles grant action?
// Fails closed on every ambiguous input — a user with no roles at all
// short-circuits to false without even touching the database. Roles and
// permissions come only from the database (master prompt §3.4/§4) —
// never from the JWT's role claim, a request header, or any other
// client-supplied value; user must already be the trusted, server-
// resolved auth.AuthenticatedUser attached by internal/middleware.Auth.
func (s *Service) HasPermission(ctx context.Context, user auth.AuthenticatedUser, action Action) (bool, error) {
	if len(user.Roles) == 0 {
		return false, nil
	}
	perms, err := s.loadPermissions(ctx, user.Roles)
	if err != nil {
		return false, err
	}
	return perms.Has(action), nil
}

// recordDenied integrates an authorization denial with the existing audit
// abstraction (master prompt §22). It never logs a password, token, or
// document content — only identifiers and the internal decision reason
// already defined not to be client-facing (see Decision's doc comment).
func (s *Service) recordDenied(ctx context.Context, user auth.AuthenticatedUser, action Action, resourceType string, resourceID *uuid.UUID, caseID *uuid.UUID, reason string) {
	s.recorder.Record(ctx, audit.Event{
		Action:       "AUTHZ_DENIED",
		ResourceType: resourceType,
		ResourceID:   resourceID,
		UserID:       &user.ID,
		Role:         effectiveRole(user),
		CaseID:       caseID,
		Metadata:     map[string]any{"action": string(action), "reason": reason},
	})
}
