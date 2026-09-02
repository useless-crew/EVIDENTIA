//go:build integration

// RBAC integration tests for System 4 (internal/authz). These exercise
// authz.Service.HasPermission/CanModifyUserRole against the REAL database —
// the actual roles/permissions/role_permissions rows seeded by
// backend/db/seed/001_reference_data.sql, not a hand-copied duplicate of
// that matrix in Go (master prompt §3.4: "Roles and permissions must come
// from the server/database"). See helpers_test.go for migratorPool/
// appPool/truncateAll and db_schema_test.go for mustInsertUser.
package tests

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"evidentia/backend/internal/audit"
	"evidentia/backend/internal/auth"
	"evidentia/backend/internal/authz"
)

// seedReferenceData loads the exact production seed file (roles,
// permissions, role_permissions) as the migrator — the single source of
// truth for "what can each role do" (see 001_reference_data.sql's own
// "not a final authorization policy" comment). Reading the real file
// rather than re-typing the matrix here means these tests fail loudly if
// the seed data and this test suite's expectations ever drift apart,
// instead of both silently agreeing with each other while disagreeing
// with production.
func seedReferenceData(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	path := filepath.Join("..", "db", "seed", "001_reference_data.sql")
	sql, err := os.ReadFile(path)
	require.NoError(t, err, "reading %s", path)
	_, err = pool.Exec(context.Background(), string(sql))
	require.NoError(t, err)
}

func assignRole(t *testing.T, pool *pgxpool.Pool, userID uuid.UUID, roleName string) {
	t.Helper()
	tag, err := pool.Exec(context.Background(),
		`INSERT INTO user_roles (user_id, role_id) SELECT $1, id FROM roles WHERE name = $2`,
		userID, roleName,
	)
	require.NoError(t, err)
	require.Equal(t, int64(1), tag.RowsAffected(), "role %q must exist in the seeded catalog", roleName)
}

func testRecorder() audit.Recorder {
	return audit.NewSlogRecorder(slog.New(slog.NewTextHandler(io.Discard, nil)))
}

// newRBACFixture truncates every table, reseeds the reference catalog, and
// creates one user with roleNames assigned — the common setup for every
// test in this file.
func newRBACFixture(t *testing.T, roleNames ...string) (userID uuid.UUID, svc *authz.Service) {
	t.Helper()
	migrator := migratorPool(t)
	truncateAll(t, migrator)
	seedReferenceData(t, migrator)

	userID = mustInsertUser(t, migrator, "rbac-"+uuid.NewString()+"@example.com")
	for _, role := range roleNames {
		assignRole(t, migrator, userID, role)
	}

	svc = authz.NewService(appPool(t), testRecorder())
	return userID, svc
}

func TestRBAC_AdminHasBroadPermissions(t *testing.T) {
	userID, svc := newRBACFixture(t, "ADMIN")
	ctx := context.Background()
	user := auth.AuthenticatedUser{ID: userID, Roles: []string{"ADMIN"}}

	for _, action := range []authz.Action{
		authz.ActionCaseCreate, authz.ActionCaseRead, authz.ActionCaseUpdate,
		authz.ActionDocumentUpload, authz.ActionDocumentDownload, authz.ActionDocumentRedact, authz.ActionDocumentShare,
		authz.ActionAuditRead, authz.ActionAuditVerify,
		authz.ActionCertificateRead, authz.ActionCertificateCreate,
		authz.ActionUserCreate, authz.ActionUserUpdate, authz.ActionUserRole,
	} {
		allowed, err := svc.HasPermission(ctx, user, action)
		require.NoError(t, err)
		assert.True(t, allowed, "ADMIN should be granted %s", action)
	}
}

func TestRBAC_PolicePermissions(t *testing.T) {
	userID, svc := newRBACFixture(t, "POLICE")
	ctx := context.Background()
	user := auth.AuthenticatedUser{ID: userID, Roles: []string{"POLICE"}}

	granted := []authz.Action{
		authz.ActionCaseCreate, authz.ActionCaseRead, authz.ActionCaseUpdate,
		authz.ActionDocumentUpload, authz.ActionDocumentRead, authz.ActionDocumentDownload, authz.ActionDocumentVerify, authz.ActionDocumentShare,
		authz.ActionAuditRead,
	}
	for _, action := range granted {
		allowed, err := svc.HasPermission(ctx, user, action)
		require.NoError(t, err)
		assert.True(t, allowed, "POLICE should be granted %s", action)
	}

	denied := []authz.Action{
		authz.ActionDocumentRedact, authz.ActionCertificateCreate, authz.ActionCertificateRead,
		authz.ActionUserCreate, authz.ActionUserRole, authz.ActionAuditVerify,
	}
	for _, action := range denied {
		allowed, err := svc.HasPermission(ctx, user, action)
		require.NoError(t, err)
		assert.False(t, allowed, "POLICE should NOT be granted %s", action)
	}
}

func TestRBAC_ForensicsPermissions(t *testing.T) {
	userID, svc := newRBACFixture(t, "FORENSICS")
	ctx := context.Background()
	user := auth.AuthenticatedUser{ID: userID, Roles: []string{"FORENSICS"}}

	granted := []authz.Action{
		authz.ActionCaseRead,
		authz.ActionDocumentUpload, authz.ActionDocumentRead, authz.ActionDocumentDownload, authz.ActionDocumentVerify,
	}
	for _, action := range granted {
		allowed, err := svc.HasPermission(ctx, user, action)
		require.NoError(t, err)
		assert.True(t, allowed, "FORENSICS should be granted %s", action)
	}

	denied := []authz.Action{
		authz.ActionCaseCreate, authz.ActionCaseUpdate, authz.ActionDocumentShare, authz.ActionDocumentRedact,
		authz.ActionUserRole, authz.ActionAuditRead,
	}
	for _, action := range denied {
		allowed, err := svc.HasPermission(ctx, user, action)
		require.NoError(t, err)
		assert.False(t, allowed, "FORENSICS should NOT be granted %s", action)
	}
}

func TestRBAC_LawyerPermissions(t *testing.T) {
	userID, svc := newRBACFixture(t, "LAWYER")
	ctx := context.Background()
	user := auth.AuthenticatedUser{ID: userID, Roles: []string{"LAWYER"}}

	granted := []authz.Action{
		authz.ActionCaseRead,
		authz.ActionDocumentRead, authz.ActionDocumentDownload, authz.ActionDocumentShare,
		authz.ActionAuditRead,
	}
	for _, action := range granted {
		allowed, err := svc.HasPermission(ctx, user, action)
		require.NoError(t, err)
		assert.True(t, allowed, "LAWYER should be granted %s", action)
	}

	denied := []authz.Action{
		authz.ActionCaseCreate, authz.ActionCaseUpdate,
		authz.ActionDocumentUpload, authz.ActionDocumentVerify, authz.ActionDocumentRedact,
		authz.ActionUserRole, authz.ActionCertificateRead,
	}
	for _, action := range denied {
		allowed, err := svc.HasPermission(ctx, user, action)
		require.NoError(t, err)
		assert.False(t, allowed, "LAWYER should NOT be granted %s", action)
	}
}

func TestRBAC_JudgePermissions(t *testing.T) {
	userID, svc := newRBACFixture(t, "JUDGE")
	ctx := context.Background()
	user := auth.AuthenticatedUser{ID: userID, Roles: []string{"JUDGE"}}

	granted := []authz.Action{
		authz.ActionCaseRead,
		authz.ActionDocumentRead, authz.ActionDocumentDownload,
		authz.ActionCertificateRead,
		authz.ActionAuditRead,
	}
	for _, action := range granted {
		allowed, err := svc.HasPermission(ctx, user, action)
		require.NoError(t, err)
		assert.True(t, allowed, "JUDGE should be granted %s", action)
	}

	denied := []authz.Action{
		authz.ActionCaseCreate, authz.ActionCaseUpdate,
		authz.ActionDocumentUpload, authz.ActionDocumentShare, authz.ActionDocumentRedact,
		authz.ActionUserRole, authz.ActionCertificateCreate,
	}
	for _, action := range denied {
		allowed, err := svc.HasPermission(ctx, user, action)
		require.NoError(t, err)
		assert.False(t, allowed, "JUDGE should NOT be granted %s", action)
	}
}

// TestRBAC_MultiRoleUserGetsUnionOfPermissions is master prompt §15/§31's
// multi-role requirement: a user holding two roles gets the UNION of both
// roles' permissions — never just one, and never something a client could
// have selected.
func TestRBAC_MultiRoleUserGetsUnionOfPermissions(t *testing.T) {
	userID, svc := newRBACFixture(t, "LAWYER", "FORENSICS")
	ctx := context.Background()
	user := auth.AuthenticatedUser{ID: userID, Roles: []string{"FORENSICS", "LAWYER"}}

	// document:share comes only from LAWYER; document:upload comes only
	// from FORENSICS. Both must be granted — proving a real union, not
	// "whichever role happened to be checked first" or "only the first
	// role in the slice".
	allowedShare, err := svc.HasPermission(ctx, user, authz.ActionDocumentShare)
	require.NoError(t, err)
	assert.True(t, allowedShare, "expected document:share via the LAWYER role")

	allowedUpload, err := svc.HasPermission(ctx, user, authz.ActionDocumentUpload)
	require.NoError(t, err)
	assert.True(t, allowedUpload, "expected document:upload via the FORENSICS role")

	// Neither role alone grants case:create.
	allowedCreate, err := svc.HasPermission(ctx, user, authz.ActionCaseCreate)
	require.NoError(t, err)
	assert.False(t, allowedCreate, "neither LAWYER nor FORENSICS grants case:create")
}

// TestRBAC_UnknownRoleNameGrantsNothing proves that a client-influenced or
// stale role name (one no longer present in the catalog) fails closed for
// itself instead of erroring the whole check or being silently ignored in
// a way that grants access.
func TestRBAC_UnknownRoleNameGrantsNothing(t *testing.T) {
	_, svc := newRBACFixture(t)
	ctx := context.Background()
	user := auth.AuthenticatedUser{ID: uuid.New(), Roles: []string{"SUPERADMIN"}}

	allowed, err := svc.HasPermission(ctx, user, authz.ActionCaseCreate)
	require.NoError(t, err)
	assert.False(t, allowed)
}

// ---- Privilege escalation (master prompt §26/§27) ----

func TestRBAC_OnlyAdminCanModifyRoles(t *testing.T) {
	target := uuid.New()
	ctx := context.Background()

	for _, role := range []string{"POLICE", "FORENSICS", "LAWYER", "JUDGE"} {
		t.Run(role, func(t *testing.T) {
			userID, svc := newRBACFixture(t, role)
			actor := auth.AuthenticatedUser{ID: userID, Roles: []string{role}}
			decision, err := svc.CanModifyUserRole(ctx, actor, target)
			require.NoError(t, err)
			assert.False(t, decision.Allowed, "%s must not be able to modify another user's role", role)
		})
	}
}

// TestRBAC_AdminCannotSelfEscalateThroughRoleModification is the explicit
// self-escalation guard: even ADMIN (the one role actually granted
// user:role) is blocked from using that exact operation on their OWN
// account (master prompt §26: "Only authorized administrative users can
// modify roles" combined with "no self-service privilege escalation").
func TestRBAC_AdminCannotSelfEscalateThroughRoleModification(t *testing.T) {
	userID, svc := newRBACFixture(t, "ADMIN")
	actor := auth.AuthenticatedUser{ID: userID, Roles: []string{"ADMIN"}}

	decision, err := svc.CanModifyUserRole(context.Background(), actor, userID)
	require.NoError(t, err)
	assert.False(t, decision.Allowed, "an admin must not be able to modify their OWN role through this operation")
}

func TestRBAC_AdminCanModifyAnotherUsersRole(t *testing.T) {
	userID, svc := newRBACFixture(t, "ADMIN")
	actor := auth.AuthenticatedUser{ID: userID, Roles: []string{"ADMIN"}}

	decision, err := svc.CanModifyUserRole(context.Background(), actor, uuid.New())
	require.NoError(t, err)
	assert.True(t, decision.Allowed)
}
