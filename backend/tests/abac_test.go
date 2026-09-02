//go:build integration

// ABAC integration tests for System 4 (internal/authz): case- and
// document-based attribute checks layered on top of the RBAC checks
// already covered by rbac_test.go, exercised against the real database
// (and therefore, incidentally, against the real RLS policies these
// queries run under — see internal/authz/case_policy.go's doc comment on
// why that's deliberate). See helpers_test.go for migratorPool/appPool/
// truncateAll and db_schema_test.go for mustInsertUser/mustInsertCase/
// mustInsertDocument.
package tests

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"evidentia/backend/internal/auth"
	"evidentia/backend/internal/authz"
	"evidentia/backend/internal/models"
)

func addCaseMember(t *testing.T, pool *pgxpool.Pool, caseID, userID, addedBy uuid.UUID, membershipType string) {
	t.Helper()
	_, err := pool.Exec(context.Background(),
		`INSERT INTO case_members (case_id, user_id, membership_type, added_by) VALUES ($1, $2, $3, $4)`,
		caseID, userID, membershipType, addedBy,
	)
	require.NoError(t, err)
}

func removeCaseMember(t *testing.T, pool *pgxpool.Pool, caseID, userID uuid.UUID) {
	t.Helper()
	_, err := pool.Exec(context.Background(),
		`UPDATE case_members SET removed_at = now() WHERE case_id = $1 AND user_id = $2`,
		caseID, userID,
	)
	require.NoError(t, err)
}

// abacFixture truncates every table, reseeds the reference catalog, and
// returns ready-to-use pools — the common setup for every test below.
func abacFixture(t *testing.T) (migrator, app *pgxpool.Pool, svc *authz.Service) {
	t.Helper()
	migrator = migratorPool(t)
	truncateAll(t, migrator)
	seedReferenceData(t, migrator)
	app = appPool(t)
	svc = authz.NewService(app, testRecorder())
	return migrator, app, svc
}

func userWithRole(t *testing.T, migrator *pgxpool.Pool, email, role string) auth.AuthenticatedUser {
	t.Helper()
	id := mustInsertUser(t, migrator, email)
	assignRole(t, migrator, id, role)
	return auth.AuthenticatedUser{ID: id, Roles: []string{role}}
}

// ---- Case ABAC ----

func TestABAC_CaseCreatorCanAccessOwnCaseBeforeAnyMembershipRow(t *testing.T) {
	// Regression-shaped test mirroring db_rls_test.go's bootstrap case:
	// the case's creator must be recognized via cases.created_by alone,
	// with no case_members row required yet.
	migrator, _, svc := abacFixture(t)
	police := userWithRole(t, migrator, "police-owner@example.com", "POLICE")
	caseID := mustInsertCase(t, migrator, "ABAC-OWNER-1", police.ID)

	decision, err := svc.CanAccessCase(context.Background(), police, caseID, authz.ActionCaseRead)
	require.NoError(t, err)
	assert.True(t, decision.Allowed)
}

func TestABAC_CaseMemberCanAccessCase(t *testing.T) {
	migrator, _, svc := abacFixture(t)
	police := userWithRole(t, migrator, "police-creator@example.com", "POLICE")
	forensics := userWithRole(t, migrator, "forensics-member@example.com", "FORENSICS")
	caseID := mustInsertCase(t, migrator, "ABAC-MEMBER-1", police.ID)
	addCaseMember(t, migrator, caseID, forensics.ID, police.ID, models.MembershipTypeForensics)

	decision, err := svc.CanAccessCase(context.Background(), forensics, caseID, authz.ActionCaseRead)
	require.NoError(t, err)
	assert.True(t, decision.Allowed)
}

func TestABAC_NonMemberDeniedCaseAccess(t *testing.T) {
	migrator, _, svc := abacFixture(t)
	owner := userWithRole(t, migrator, "owner2@example.com", "POLICE")
	outsider := userWithRole(t, migrator, "outsider@example.com", "POLICE")
	caseID := mustInsertCase(t, migrator, "ABAC-NONMEMBER-1", owner.ID)

	decision, err := svc.CanAccessCase(context.Background(), outsider, caseID, authz.ActionCaseRead)
	require.NoError(t, err)
	assert.False(t, decision.Allowed, "a POLICE user with no relationship to this specific case must be denied")
}

func TestABAC_LawyerAssignedCaseAccessGranted(t *testing.T) {
	migrator, _, svc := abacFixture(t)
	police := userWithRole(t, migrator, "police-for-lawyer@example.com", "POLICE")
	lawyer := userWithRole(t, migrator, "lawyer-assigned@example.com", "LAWYER")
	caseID := mustInsertCase(t, migrator, "ABAC-LAWYER-1", police.ID)
	addCaseMember(t, migrator, caseID, lawyer.ID, police.ID, models.MembershipTypeLawyer)

	decision, err := svc.CanAccessCase(context.Background(), lawyer, caseID, authz.ActionCaseRead)
	require.NoError(t, err)
	assert.True(t, decision.Allowed)
}

// TestABAC_LawyerUnrelatedCaseDenied is master prompt §8's central LAWYER
// example: being attached to ANY case must not imply access to a
// DIFFERENT case the lawyer was never assigned to.
func TestABAC_LawyerUnrelatedCaseDenied(t *testing.T) {
	migrator, _, svc := abacFixture(t)
	police := userWithRole(t, migrator, "police-for-lawyer2@example.com", "POLICE")
	lawyer := userWithRole(t, migrator, "lawyer-unrelated@example.com", "LAWYER")
	assignedCase := mustInsertCase(t, migrator, "ABAC-LAWYER-2A", police.ID)
	unrelatedCase := mustInsertCase(t, migrator, "ABAC-LAWYER-2B", police.ID)
	addCaseMember(t, migrator, assignedCase, lawyer.ID, police.ID, models.MembershipTypeLawyer)

	decision, err := svc.CanAccessCase(context.Background(), lawyer, unrelatedCase, authz.ActionCaseRead)
	require.NoError(t, err)
	assert.False(t, decision.Allowed)
}

func TestABAC_ForensicsLinkedCaseAccessGranted(t *testing.T) {
	migrator, _, svc := abacFixture(t)
	police := userWithRole(t, migrator, "police-for-forensics@example.com", "POLICE")
	forensics := userWithRole(t, migrator, "forensics-linked@example.com", "FORENSICS")
	caseID := mustInsertCase(t, migrator, "ABAC-FORENSICS-1", police.ID)
	addCaseMember(t, migrator, caseID, forensics.ID, police.ID, models.MembershipTypeForensics)

	decision, err := svc.CanAccessCase(context.Background(), forensics, caseID, authz.ActionCaseRead)
	require.NoError(t, err)
	assert.True(t, decision.Allowed)
}

func TestABAC_ForensicsUnrelatedCaseDenied(t *testing.T) {
	migrator, _, svc := abacFixture(t)
	police := userWithRole(t, migrator, "police-for-forensics2@example.com", "POLICE")
	forensics := userWithRole(t, migrator, "forensics-unrelated@example.com", "FORENSICS")
	caseID := mustInsertCase(t, migrator, "ABAC-FORENSICS-2", police.ID)

	decision, err := svc.CanAccessCase(context.Background(), forensics, caseID, authz.ActionCaseRead)
	require.NoError(t, err)
	assert.False(t, decision.Allowed)
}

func TestABAC_JudgeAuthorizedCaseAccessGranted(t *testing.T) {
	migrator, _, svc := abacFixture(t)
	police := userWithRole(t, migrator, "police-for-judge@example.com", "POLICE")
	judge := userWithRole(t, migrator, "judge-authorized@example.com", "JUDGE")
	caseID := mustInsertCase(t, migrator, "ABAC-JUDGE-1", police.ID)
	addCaseMember(t, migrator, caseID, judge.ID, police.ID, models.MembershipTypeJudge)

	decision, err := svc.CanAccessCase(context.Background(), judge, caseID, authz.ActionCaseRead)
	require.NoError(t, err)
	assert.True(t, decision.Allowed)
}

func TestABAC_JudgeUnauthorizedCaseDenied(t *testing.T) {
	migrator, _, svc := abacFixture(t)
	police := userWithRole(t, migrator, "police-for-judge2@example.com", "POLICE")
	judge := userWithRole(t, migrator, "judge-unauthorized@example.com", "JUDGE")
	caseID := mustInsertCase(t, migrator, "ABAC-JUDGE-2", police.ID)

	decision, err := svc.CanAccessCase(context.Background(), judge, caseID, authz.ActionCaseRead)
	require.NoError(t, err)
	assert.False(t, decision.Allowed)
}

func TestABAC_AdminBypassesCaseMembership(t *testing.T) {
	migrator, _, svc := abacFixture(t)
	police := userWithRole(t, migrator, "police-for-admin@example.com", "POLICE")
	admin := userWithRole(t, migrator, "admin-bypass@example.com", "ADMIN")
	caseID := mustInsertCase(t, migrator, "ABAC-ADMIN-1", police.ID)

	decision, err := svc.CanAccessCase(context.Background(), admin, caseID, authz.ActionCaseRead)
	require.NoError(t, err)
	assert.True(t, decision.Allowed, "ADMIN must be able to access a case it has no membership row for")
}

func TestABAC_RemovedMembershipDeniesAccess(t *testing.T) {
	migrator, _, svc := abacFixture(t)
	police := userWithRole(t, migrator, "police-for-removed@example.com", "POLICE")
	lawyer := userWithRole(t, migrator, "lawyer-removed@example.com", "LAWYER")
	caseID := mustInsertCase(t, migrator, "ABAC-REMOVED-1", police.ID)
	addCaseMember(t, migrator, caseID, lawyer.ID, police.ID, models.MembershipTypeLawyer)
	removeCaseMember(t, migrator, caseID, lawyer.ID)

	decision, err := svc.CanAccessCase(context.Background(), lawyer, caseID, authz.ActionCaseRead)
	require.NoError(t, err)
	assert.False(t, decision.Allowed, "a removed case member must be denied, not grandfathered in")
}

// TestABAC_RBACGateBlocksBeforeResourceScope proves the ordering promised
// by internal/authz/case_policy.go's doc comment: an action a role isn't
// granted AT ALL is denied even for a case the caller genuinely owns —
// case ownership is never a substitute for the underlying RBAC grant
// (master prompt §11: "ROLE PERMISSION AND RESOURCE RELATIONSHIP", never
// "ROLE OR OWNERSHIP").
func TestABAC_RBACGateBlocksBeforeResourceScope(t *testing.T) {
	migrator, _, svc := abacFixture(t)
	// LAWYER is not granted case:update by the seed data.
	lawyer := userWithRole(t, migrator, "lawyer-no-update@example.com", "LAWYER")
	caseID := mustInsertCase(t, migrator, "ABAC-RBACGATE-1", lawyer.ID)

	decision, err := svc.CanAccessCase(context.Background(), lawyer, caseID, authz.ActionCaseUpdate)
	require.NoError(t, err)
	assert.False(t, decision.Allowed)
	assert.Equal(t, "permission_denied", decision.Reason, "must be rejected at the RBAC gate, not the ABAC one")
}

// ---- IDOR (master prompt §25) ----

func TestABAC_GuessedCaseUUIDDenied(t *testing.T) {
	_, _, svc := abacFixture(t)
	// No case with this ID exists at all.
	police := auth.AuthenticatedUser{ID: uuid.New(), Roles: []string{"POLICE"}}

	decision, err := svc.CanAccessCase(context.Background(), police, uuid.New(), authz.ActionCaseRead)
	require.NoError(t, err)
	assert.False(t, decision.Allowed)
}

func TestABAC_GuessedDocumentUUIDDenied(t *testing.T) {
	_, _, svc := abacFixture(t)
	police := auth.AuthenticatedUser{ID: uuid.New(), Roles: []string{"POLICE"}}

	decision, err := svc.CanAccessDocument(context.Background(), police, uuid.New(), authz.ActionDocumentRead)
	require.NoError(t, err)
	assert.False(t, decision.Allowed)
}

// ---- Document ABAC ----

func TestABAC_DocumentAccessInheritsCaseMembership(t *testing.T) {
	migrator, _, svc := abacFixture(t)
	police := userWithRole(t, migrator, "police-doc-owner@example.com", "POLICE")
	lawyer := userWithRole(t, migrator, "lawyer-doc-member@example.com", "LAWYER")
	caseID := mustInsertCase(t, migrator, "ABAC-DOC-1", police.ID)
	addCaseMember(t, migrator, caseID, lawyer.ID, police.ID, models.MembershipTypeLawyer)
	documentID := mustInsertDocument(t, migrator, caseID, police.ID, "doc-1")

	decision, err := svc.CanAccessDocument(context.Background(), lawyer, documentID, authz.ActionDocumentRead)
	require.NoError(t, err)
	assert.True(t, decision.Allowed, "a document belonging to a case the lawyer is assigned to must be accessible")
}

// TestABAC_CrossCaseDocumentAccessDenied is master prompt §9/§25's
// explicit example: a LAWYER attached to Case A must not gain access to a
// document belonging to a DIFFERENT case (Case B), even though the lawyer
// holds document:read in general.
func TestABAC_CrossCaseDocumentAccessDenied(t *testing.T) {
	migrator, _, svc := abacFixture(t)
	police := userWithRole(t, migrator, "police-doc-owner2@example.com", "POLICE")
	lawyer := userWithRole(t, migrator, "lawyer-cross-case@example.com", "LAWYER")
	caseA := mustInsertCase(t, migrator, "ABAC-DOC-2A", police.ID)
	caseB := mustInsertCase(t, migrator, "ABAC-DOC-2B", police.ID)
	addCaseMember(t, migrator, caseA, lawyer.ID, police.ID, models.MembershipTypeLawyer)
	documentInCaseB := mustInsertDocument(t, migrator, caseB, police.ID, "doc-2b")

	decision, err := svc.CanAccessDocument(context.Background(), lawyer, documentInCaseB, authz.ActionDocumentRead)
	require.NoError(t, err)
	assert.False(t, decision.Allowed)
}

func TestABAC_DocumentUploaderWithoutCaseRelationshipStillDenied(t *testing.T) {
	// Uploading a document (as an ADMIN acting on behalf of the case, or
	// via direct fixture insertion here) does not, by itself, grant the
	// uploader ongoing access if they are never made a case member and
	// are not the case's creator — ownership of the upload action is not
	// a standing case relationship (master prompt §11: ownership is not a
	// universal bypass).
	migrator, _, svc := abacFixture(t)
	police := userWithRole(t, migrator, "police-real-owner@example.com", "POLICE")
	forensics := userWithRole(t, migrator, "forensics-uploader@example.com", "FORENSICS")
	caseID := mustInsertCase(t, migrator, "ABAC-DOC-3", police.ID)
	// forensics uploads the document directly via fixture (bypassing the
	// not-yet-implemented upload handler) but is never added as a case
	// member.
	documentID := mustInsertDocument(t, migrator, caseID, forensics.ID, "doc-3")

	decision, err := svc.CanAccessDocument(context.Background(), forensics, documentID, authz.ActionDocumentRead)
	require.NoError(t, err)
	assert.False(t, decision.Allowed, "uploading a document must not itself grant standing case access")
}

func TestABAC_AdminBypassesDocumentCaseMembership(t *testing.T) {
	migrator, _, svc := abacFixture(t)
	police := userWithRole(t, migrator, "police-doc-admin@example.com", "POLICE")
	admin := userWithRole(t, migrator, "admin-doc-bypass@example.com", "ADMIN")
	caseID := mustInsertCase(t, migrator, "ABAC-DOC-4", police.ID)
	documentID := mustInsertDocument(t, migrator, caseID, police.ID, "doc-4")

	decision, err := svc.CanAccessDocument(context.Background(), admin, documentID, authz.ActionDocumentRead)
	require.NoError(t, err)
	assert.True(t, decision.Allowed)
}
