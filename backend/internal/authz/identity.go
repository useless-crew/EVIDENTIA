package authz

import (
	"evidentia/backend/internal/auth"
	"evidentia/backend/internal/models"
)

// hasRole reports whether u's role set contains role. Comparison is exact
// against the fixed catalog seeded by backend/db/seed/001_reference_data.sql
// (ADMIN, POLICE, FORENSICS, LAWYER, JUDGE — see internal/models/role.go),
// operating only on the server-resolved auth.AuthenticatedUser.Roles —
// never a client-supplied value.
func hasRole(u auth.AuthenticatedUser, role string) bool {
	for _, r := range u.Roles {
		if r == role {
			return true
		}
	}
	return false
}

func isAdmin(u auth.AuthenticatedUser) bool {
	return hasRole(u, models.RoleAdmin)
}

// effectiveRole picks the single role name recorded on a PostgreSQL RLS
// transaction (app.role) for a multi-role user. RLS's own policies
// (current_app_role() — see db/migrations/000001_init_schema.up.sql) only
// ever special-case 'ADMIN'; every other role is treated identically by
// RLS (case_members existence is what actually gates access there), so
// which non-admin role is chosen here has no security effect on RLS
// itself — it only affects what gets recorded as the "acting role" for
// diagnostics (e.g. audit_log.role).
//
// u.Roles arrives from auth.AuthenticatedUser, itself populated from
// ListRolesForUser's "ORDER BY r.name" (see internal/service/auth_service.go's
// ResolveIdentity) — so this follows the exact same "alphabetically-first
// role" convention already established for the JWT role claim
// (auth_service.go's primaryRoleName): ADMIN sorts first whenever a user
// holds it, without this function needing its own special case.
//
// This is a display/RLS-diagnostic convention only. It never affects an
// RBAC/ABAC decision in this package, which always evaluates the user's
// FULL role set (see PermissionSet's union in Service.loadPermissions, and
// isAdmin above) — a client can never narrow themselves to a lesser
// effective role through this mechanism (master prompt §15: "the server
// determines effective permissions").
func effectiveRole(u auth.AuthenticatedUser) string {
	if len(u.Roles) == 0 {
		return ""
	}
	return u.Roles[0]
}
