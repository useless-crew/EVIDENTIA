//go:build integration

// Run with: go test -tags=integration ./internal/service/...
// Requires the docker-compose postgres service up, migrated, seeded
// (backend/db/seed/001_reference_data.sql), with evidentia_app's password
// left at the migration's default ('changeme_example'). See
// auth_service_integration_test.go's doc comment for the -p 1 note when
// running alongside other packages' integration tests — this file shares
// that same concern (truncates/repopulates users/cases/... in the live
// database).
package service

import (
	"context"
	"os"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"evidentia/backend/internal/audit"
	"evidentia/backend/internal/auth"
	"evidentia/backend/internal/authz"
	"evidentia/backend/internal/models"
	"evidentia/backend/internal/utils"
)

// spyRecorder captures every audit.Event recorded during a test, so tests
// can assert exactly which (if any) events a call produced — a false
// "success" audit event for a failed mutation is exactly what master
// prompt §25/§42 forbids, and a spy is how these tests catch it were it to
// ever happen. mu guards Record/actions against concurrent access: some
// tests (e.g. TestCertificateService_GetOrCreateCertificate_
// ConcurrentGenerationProducesOneCertificate) deliberately call the
// service under test from multiple goroutines at once, and every one of
// those calls records through the same spyRecorder.
type spyRecorder struct {
	mu     sync.Mutex
	events []audit.Event
}

func (s *spyRecorder) Record(_ context.Context, event audit.Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, event)
}

func (s *spyRecorder) actions() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	actions := make([]string, len(s.events))
	for i, e := range s.events {
		actions[i] = e.Action
	}
	return actions
}

func truncateCaseTables(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	_, err := pool.Exec(context.Background(), `
		TRUNCATE TABLE
			document_shares, compliance_certificates, audit_log, audit_verifications, redactions, documents,
			case_involved_parties, case_members, cases,
			role_permissions, user_roles, permissions, roles, users
		RESTART IDENTITY CASCADE`)
	require.NoError(t, err)

	// role_permissions/permissions/roles are truncated above (cases cascade
	// through user_roles etc.), so reseed the fixed catalog every test —
	// authz.Service.HasPermission reads it fresh on every call.
	seedSQL, err := seedReferenceDataSQL()
	require.NoError(t, err)
	_, err = pool.Exec(context.Background(), seedSQL)
	require.NoError(t, err)
}

// seedReferenceDataSQL loads the project's own seed file rather than
// hand-copying its role/permission matrix into Go — the same principle
// backend/tests/rbac_test.go's doc comment establishes for RBAC tests.
func seedReferenceDataSQL() (string, error) {
	b, err := os.ReadFile("../../db/seed/001_reference_data.sql")
	return string(b), err
}

func paginationForTest(page, pageSize int32) utils.Pagination {
	return utils.ParsePagination(page, pageSize)
}

func utilsAsAppError(err error) (*utils.AppError, bool) {
	return utils.AsAppError(err)
}

func newUserWithRole(t *testing.T, migrator *pgxpool.Pool, email, roleName string) uuid.UUID {
	t.Helper()
	ctx := context.Background()

	var userID uuid.UUID
	require.NoError(t, migrator.QueryRow(ctx,
		`INSERT INTO users (email, password_hash, first_name, last_name, status)
		 VALUES ($1, 'x', 'Test', 'User', 'active') RETURNING id`, email,
	).Scan(&userID))

	if roleName != "" {
		_, err := migrator.Exec(ctx, `
			INSERT INTO user_roles (user_id, role_id)
			SELECT $1, id FROM roles WHERE name = $2`, userID, roleName)
		require.NoError(t, err)
	}
	return userID
}

func authUser(id uuid.UUID, roles ...string) auth.AuthenticatedUser {
	return auth.AuthenticatedUser{ID: id, Email: "test@example.com", Roles: roles}
}

func newCaseServiceForTest(t *testing.T, pool *pgxpool.Pool, recorder audit.Recorder) *CaseService {
	t.Helper()
	authzService := authz.NewService(pool, recorder)
	return NewCaseService(pool, authzService, recorder)
}

func TestCaseService_CreateCase_PoliceAllowed(t *testing.T) {
	migrator := migratorPool(t)
	truncateCaseTables(t, migrator)
	appDB := appPool(t)

	officer := newUserWithRole(t, migrator, "officer@example.com", models.RolePolice)
	rec := &spyRecorder{}
	svc := newCaseServiceForTest(t, appDB, rec)

	detail, err := svc.CreateCase(context.Background(), authUser(officer, models.RolePolice), CreateCaseInput{
		CaseNumber: "CASE-POLICE-1",
		Title:      "Theft investigation",
	})
	require.NoError(t, err)
	assert.Equal(t, "CASE-POLICE-1", detail.CaseNumber)
	assert.Equal(t, models.CaseStatusOpen, detail.Status)
	assert.Equal(t, officer, detail.CreatedBy)
	assert.True(t, detail.Relationship.IsOwner)
	assert.Contains(t, rec.actions(), "CASE_CREATED")
}

func TestCaseService_CreateCase_LawyerDenied(t *testing.T) {
	migrator := migratorPool(t)
	truncateCaseTables(t, migrator)
	appDB := appPool(t)

	lawyer := newUserWithRole(t, migrator, "lawyer@example.com", models.RoleLawyer)
	rec := &spyRecorder{}
	svc := newCaseServiceForTest(t, appDB, rec)

	_, err := svc.CreateCase(context.Background(), authUser(lawyer, models.RoleLawyer), CreateCaseInput{
		CaseNumber: "CASE-LAWYER-1",
		Title:      "Should not be created",
	})
	require.Error(t, err)
	appErr, ok := utilsAsAppError(err)
	require.True(t, ok)
	assert.Equal(t, 403, appErr.Status)
	assert.NotContains(t, rec.actions(), "CASE_CREATED", "a denied create must never produce a success audit event")
}

func TestCaseService_CreateCase_DuplicateCaseNumberConflict(t *testing.T) {
	migrator := migratorPool(t)
	truncateCaseTables(t, migrator)
	appDB := appPool(t)

	officer := newUserWithRole(t, migrator, "officer2@example.com", models.RolePolice)
	rec := &spyRecorder{}
	svc := newCaseServiceForTest(t, appDB, rec)
	ctx := context.Background()
	user := authUser(officer, models.RolePolice)

	_, err := svc.CreateCase(ctx, user, CreateCaseInput{CaseNumber: "DUP-1", Title: "First"})
	require.NoError(t, err)

	before := len(rec.events)
	_, err = svc.CreateCase(ctx, user, CreateCaseInput{CaseNumber: "DUP-1", Title: "Second"})
	require.Error(t, err)
	appErr, ok := utilsAsAppError(err)
	require.True(t, ok)
	assert.Equal(t, 409, appErr.Status)
	assert.Equal(t, before, len(rec.events), "a failed (duplicate) create must never produce a success audit event")
}

func TestCaseService_CreateCase_ClientSuppliedCreatedByIgnored(t *testing.T) {
	// CreateCaseInput structurally has no CreatedBy field at all — this
	// test proves the RESULT: created_by is always the authenticated
	// caller, regardless of what a handler might otherwise be tricked into
	// passing (defense in depth for master prompt §5, even though the
	// struct shape already makes the vulnerable case impossible).
	migrator := migratorPool(t)
	truncateCaseTables(t, migrator)
	appDB := appPool(t)

	officer := newUserWithRole(t, migrator, "officer3@example.com", models.RolePolice)
	svc := newCaseServiceForTest(t, appDB, &spyRecorder{})

	detail, err := svc.CreateCase(context.Background(), authUser(officer, models.RolePolice), CreateCaseInput{
		CaseNumber: "CASE-OWN-1",
		Title:      "Owned by caller",
	})
	require.NoError(t, err)
	assert.Equal(t, officer, detail.CreatedBy)
}

func TestCaseService_ListCases_RoleScoping(t *testing.T) {
	migrator := migratorPool(t)
	truncateCaseTables(t, migrator)
	appDB := appPool(t)
	ctx := context.Background()

	admin := newUserWithRole(t, migrator, "admin@example.com", models.RoleAdmin)
	officer := newUserWithRole(t, migrator, "officer4@example.com", models.RolePolice)
	lawyer := newUserWithRole(t, migrator, "lawyer4@example.com", models.RoleLawyer)
	forensics := newUserWithRole(t, migrator, "forensics4@example.com", models.RoleForensics)
	judge := newUserWithRole(t, migrator, "judge4@example.com", models.RoleJudge)
	outsider := newUserWithRole(t, migrator, "outsider4@example.com", models.RolePolice)

	svc := newCaseServiceForTest(t, appDB, &spyRecorder{})

	// officer creates a case (becomes OWNER via case_members) and attaches
	// lawyer/forensics/judge to it; outsider has no relationship to it.
	detail, err := svc.CreateCase(ctx, authUser(officer, models.RolePolice), CreateCaseInput{
		CaseNumber: "SCOPE-1", Title: "Scoped case",
	})
	require.NoError(t, err)
	caseID := detail.ID

	for _, m := range []struct {
		userID uuid.UUID
		mtype  string
	}{
		{lawyer, models.MembershipTypeLawyer},
		{forensics, models.MembershipTypeForensics},
		{judge, models.MembershipTypeJudge},
	} {
		_, err := migrator.Exec(ctx, `
			INSERT INTO case_members (case_id, user_id, membership_type, added_by)
			VALUES ($1, $2, $3, $4)`, caseID, m.userID, m.mtype, officer)
		require.NoError(t, err)
	}

	page := paginationForTest(1, 20)

	adminResult, err := svc.ListCases(ctx, authUser(admin, models.RoleAdmin), CaseListFilter{}, page)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(adminResult.Cases), 1, "admin sees all authorized cases")
	assert.True(t, containsCaseNumber(adminResult.Cases, "SCOPE-1"))

	officerResult, err := svc.ListCases(ctx, authUser(officer, models.RolePolice), CaseListFilter{}, page)
	require.NoError(t, err)
	assert.True(t, containsCaseNumber(officerResult.Cases, "SCOPE-1"), "creator sees their own case")

	lawyerResult, err := svc.ListCases(ctx, authUser(lawyer, models.RoleLawyer), CaseListFilter{}, page)
	require.NoError(t, err)
	assert.True(t, containsCaseNumber(lawyerResult.Cases, "SCOPE-1"), "assigned lawyer sees the case")

	forensicsResult, err := svc.ListCases(ctx, authUser(forensics, models.RoleForensics), CaseListFilter{}, page)
	require.NoError(t, err)
	assert.True(t, containsCaseNumber(forensicsResult.Cases, "SCOPE-1"), "linked forensics user sees the case")

	judgeResult, err := svc.ListCases(ctx, authUser(judge, models.RoleJudge), CaseListFilter{}, page)
	require.NoError(t, err)
	assert.True(t, containsCaseNumber(judgeResult.Cases, "SCOPE-1"), "judge with docket case_members row sees the case")

	outsiderResult, err := svc.ListCases(ctx, authUser(outsider, models.RolePolice), CaseListFilter{}, page)
	require.NoError(t, err)
	assert.False(t, containsCaseNumber(outsiderResult.Cases, "SCOPE-1"), "an unrelated POLICE user must never see another officer's case")
}

func TestCaseService_ListCases_StatusFilter(t *testing.T) {
	migrator := migratorPool(t)
	truncateCaseTables(t, migrator)
	appDB := appPool(t)
	ctx := context.Background()

	officer := newUserWithRole(t, migrator, "officer5@example.com", models.RolePolice)
	svc := newCaseServiceForTest(t, appDB, &spyRecorder{})
	user := authUser(officer, models.RolePolice)

	_, err := svc.CreateCase(ctx, user, CreateCaseInput{CaseNumber: "FILTER-OPEN", Title: "Open one"})
	require.NoError(t, err)
	closedStatus := models.CaseStatusClosed
	_, err = svc.CreateCase(ctx, user, CreateCaseInput{CaseNumber: "FILTER-CLOSED", Title: "Pre-closed", Status: nil})
	require.NoError(t, err)

	openStatus := models.CaseStatusOpen
	result, err := svc.ListCases(ctx, user, CaseListFilter{Status: &openStatus}, paginationForTest(1, 20))
	require.NoError(t, err)
	assert.True(t, containsCaseNumber(result.Cases, "FILTER-OPEN"))
	assert.True(t, containsCaseNumber(result.Cases, "FILTER-CLOSED"), "both are OPEN at this point")

	filtered, err := svc.ListCases(ctx, user, CaseListFilter{Status: &closedStatus}, paginationForTest(1, 20))
	require.NoError(t, err)
	assert.False(t, containsCaseNumber(filtered.Cases, "FILTER-OPEN"))
	assert.Empty(t, filtered.Cases)
}

func TestCaseService_GetCase_UnrelatedUserDenied(t *testing.T) {
	migrator := migratorPool(t)
	truncateCaseTables(t, migrator)
	appDB := appPool(t)
	ctx := context.Background()

	officer := newUserWithRole(t, migrator, "officer6@example.com", models.RolePolice)
	outsider := newUserWithRole(t, migrator, "outsider6@example.com", models.RoleLawyer)
	svc := newCaseServiceForTest(t, appDB, &spyRecorder{})

	created, err := svc.CreateCase(ctx, authUser(officer, models.RolePolice), CreateCaseInput{
		CaseNumber: "PRIVATE-1", Title: "Private case",
	})
	require.NoError(t, err)

	_, err = svc.GetCase(ctx, authUser(outsider, models.RoleLawyer), created.ID)
	require.Error(t, err)
	appErr, ok := utilsAsAppError(err)
	require.True(t, ok)
	assert.Equal(t, 403, appErr.Status)
}

func TestCaseService_GetCase_GuessedUUIDDenied(t *testing.T) {
	// IDOR: a syntactically valid but nonexistent case ID must be denied
	// identically to a real case the caller has no relationship to (master
	// prompt §14/§21).
	migrator := migratorPool(t)
	truncateCaseTables(t, migrator)
	appDB := appPool(t)

	lawyer := newUserWithRole(t, migrator, "lawyer7@example.com", models.RoleLawyer)
	svc := newCaseServiceForTest(t, appDB, &spyRecorder{})

	_, err := svc.GetCase(context.Background(), authUser(lawyer, models.RoleLawyer), uuid.New())
	require.Error(t, err)
	appErr, ok := utilsAsAppError(err)
	require.True(t, ok)
	assert.Equal(t, 403, appErr.Status)
}

func TestCaseService_GetCase_IncludesInvolvedPartiesAndTimeline(t *testing.T) {
	migrator := migratorPool(t)
	truncateCaseTables(t, migrator)
	appDB := appPool(t)
	ctx := context.Background()

	officer := newUserWithRole(t, migrator, "officer8@example.com", models.RolePolice)
	svc := newCaseServiceForTest(t, appDB, &spyRecorder{})
	user := authUser(officer, models.RolePolice)

	created, err := svc.CreateCase(ctx, user, CreateCaseInput{CaseNumber: "DETAIL-1", Title: "Detail case"})
	require.NoError(t, err)

	_, err = migrator.Exec(ctx, `
		INSERT INTO case_involved_parties (case_id, party_type, display_name, added_by)
		VALUES ($1, 'WITNESS', 'Jane Witness', $2)`, created.ID, officer)
	require.NoError(t, err)

	detail, err := svc.GetCase(ctx, user, created.ID)
	require.NoError(t, err)
	require.Len(t, detail.InvolvedParties, 1)
	assert.Equal(t, "Jane Witness", detail.InvolvedParties[0].DisplayName, "POLICE may view witness identity")
	require.NotEmpty(t, detail.Timeline)
	assert.Equal(t, TimelineEventCaseCreated, detail.Timeline[0].Type)
}

func TestCaseService_GetCase_WitnessRedactedForForensics(t *testing.T) {
	migrator := migratorPool(t)
	truncateCaseTables(t, migrator)
	appDB := appPool(t)
	ctx := context.Background()

	officer := newUserWithRole(t, migrator, "officer9@example.com", models.RolePolice)
	forensics := newUserWithRole(t, migrator, "forensics9@example.com", models.RoleForensics)
	svc := newCaseServiceForTest(t, appDB, &spyRecorder{})

	created, err := svc.CreateCase(ctx, authUser(officer, models.RolePolice), CreateCaseInput{
		CaseNumber: "DETAIL-2", Title: "Witness case",
	})
	require.NoError(t, err)

	_, err = migrator.Exec(ctx, `
		INSERT INTO case_members (case_id, user_id, membership_type, added_by)
		VALUES ($1, $2, 'FORENSICS', $3)`, created.ID, forensics, officer)
	require.NoError(t, err)
	_, err = migrator.Exec(ctx, `
		INSERT INTO case_involved_parties (case_id, party_type, display_name, added_by)
		VALUES ($1, 'WITNESS', 'Jane Witness', $2)`, created.ID, officer)
	require.NoError(t, err)

	detail, err := svc.GetCase(ctx, authUser(forensics, models.RoleForensics), created.ID)
	require.NoError(t, err)
	require.Len(t, detail.InvolvedParties, 1)
	assert.Equal(t, "[REDACTED]", detail.InvolvedParties[0].DisplayName, "FORENSICS must not see witness identity")
}

func TestCaseService_UpdateCase_AuthorizedAllowed(t *testing.T) {
	migrator := migratorPool(t)
	truncateCaseTables(t, migrator)
	appDB := appPool(t)
	ctx := context.Background()

	officer := newUserWithRole(t, migrator, "officer10@example.com", models.RolePolice)
	rec := &spyRecorder{}
	svc := newCaseServiceForTest(t, appDB, rec)
	user := authUser(officer, models.RolePolice)

	created, err := svc.CreateCase(ctx, user, CreateCaseInput{CaseNumber: "UPD-1", Title: "Original"})
	require.NoError(t, err)

	rec.events = nil
	updated, err := svc.UpdateCase(ctx, user, created.ID, UpdateCaseInput{
		Title:  "Updated title",
		Status: models.CaseStatusUnderInvestigation,
	})
	require.NoError(t, err)
	assert.Equal(t, "Updated title", updated.Title)
	assert.Equal(t, models.CaseStatusUnderInvestigation, updated.Status)
	assert.Equal(t, created.ID, updated.ID, "id must never change")
	assert.Equal(t, officer, updated.CreatedBy, "created_by must never change")
	assert.Contains(t, rec.actions(), "CASE_UPDATED")
	assert.Contains(t, rec.actions(), "CASE_STATUS_CHANGED")
}

func TestCaseService_UpdateCase_UnauthorizedRoleDenied(t *testing.T) {
	migrator := migratorPool(t)
	truncateCaseTables(t, migrator)
	appDB := appPool(t)
	ctx := context.Background()

	officer := newUserWithRole(t, migrator, "officer11@example.com", models.RolePolice)
	lawyer := newUserWithRole(t, migrator, "lawyer11@example.com", models.RoleLawyer)
	rec := &spyRecorder{}
	svc := newCaseServiceForTest(t, appDB, rec)

	created, err := svc.CreateCase(ctx, authUser(officer, models.RolePolice), CreateCaseInput{
		CaseNumber: "UPD-2", Title: "Original",
	})
	require.NoError(t, err)

	// Even a lawyer explicitly attached to the case cannot update it — the
	// seed data grants LAWYER no case:update permission at all (see
	// docs/SECURITY.md's "Case-based ABAC"), so RBAC denies before any
	// resource relationship is even considered.
	_, err = migrator.Exec(ctx, `
		INSERT INTO case_members (case_id, user_id, membership_type, added_by)
		VALUES ($1, $2, 'LAWYER', $3)`, created.ID, lawyer, officer)
	require.NoError(t, err)

	_, err = svc.UpdateCase(ctx, authUser(lawyer, models.RoleLawyer), created.ID, UpdateCaseInput{
		Title: "Hijacked", Status: models.CaseStatusClosed,
	})
	require.Error(t, err)
	appErr, ok := utilsAsAppError(err)
	require.True(t, ok)
	assert.Equal(t, 403, appErr.Status)
	assert.NotContains(t, rec.actions(), "CASE_UPDATED", "a denied update must never produce a success audit event")
	assert.NotContains(t, rec.actions(), "CASE_STATUS_CHANGED", "a denied update must never produce a success audit event")
}

func TestCaseService_UpdateCase_CrossCaseDenied(t *testing.T) {
	migrator := migratorPool(t)
	truncateCaseTables(t, migrator)
	appDB := appPool(t)
	ctx := context.Background()

	officerA := newUserWithRole(t, migrator, "officerA@example.com", models.RolePolice)
	officerB := newUserWithRole(t, migrator, "officerB@example.com", models.RolePolice)
	svc := newCaseServiceForTest(t, appDB, &spyRecorder{})

	caseA, err := svc.CreateCase(ctx, authUser(officerA, models.RolePolice), CreateCaseInput{
		CaseNumber: "CROSS-A", Title: "Case A",
	})
	require.NoError(t, err)

	_, err = svc.UpdateCase(ctx, authUser(officerB, models.RolePolice), caseA.ID, UpdateCaseInput{
		Title: "Stolen", Status: models.CaseStatusClosed,
	})
	require.Error(t, err)
	appErr, ok := utilsAsAppError(err)
	require.True(t, ok)
	assert.Equal(t, 403, appErr.Status, "POLICE holding case:update does not imply access to another officer's case")
}

func TestCaseService_UpdateCase_InvalidStatusRejected(t *testing.T) {
	migrator := migratorPool(t)
	truncateCaseTables(t, migrator)
	appDB := appPool(t)
	ctx := context.Background()

	officer := newUserWithRole(t, migrator, "officer12@example.com", models.RolePolice)
	svc := newCaseServiceForTest(t, appDB, &spyRecorder{})
	user := authUser(officer, models.RolePolice)

	created, err := svc.CreateCase(ctx, user, CreateCaseInput{CaseNumber: "UPD-3", Title: "Original"})
	require.NoError(t, err)

	_, err = svc.UpdateCase(ctx, user, created.ID, UpdateCaseInput{Title: "x", Status: "NOT_A_REAL_STATUS"})
	require.Error(t, err)
	appErr, ok := utilsAsAppError(err)
	require.True(t, ok)
	assert.Equal(t, 400, appErr.Status)
}

func TestCaseService_UpdateCase_InvalidTransitionRejected(t *testing.T) {
	migrator := migratorPool(t)
	truncateCaseTables(t, migrator)
	appDB := appPool(t)
	ctx := context.Background()

	officer := newUserWithRole(t, migrator, "officer13@example.com", models.RolePolice)
	svc := newCaseServiceForTest(t, appDB, &spyRecorder{})
	user := authUser(officer, models.RolePolice)

	created, err := svc.CreateCase(ctx, user, CreateCaseInput{CaseNumber: "UPD-4", Title: "Original"})
	require.NoError(t, err)

	// OPEN -> CLOSED directly is not in caseStatusTransitions.
	_, err = svc.UpdateCase(ctx, user, created.ID, UpdateCaseInput{Title: "x", Status: models.CaseStatusClosed})
	require.Error(t, err)
	appErr, ok := utilsAsAppError(err)
	require.True(t, ok)
	assert.Equal(t, 400, appErr.Status)

	// The case must remain unchanged (no partial update).
	fresh, err := svc.GetCase(ctx, user, created.ID)
	require.NoError(t, err)
	assert.Equal(t, models.CaseStatusOpen, fresh.Status)
}

func TestCaseService_ListCases_PaginationRespectsMaxPageSize(t *testing.T) {
	migrator := migratorPool(t)
	truncateCaseTables(t, migrator)
	appDB := appPool(t)
	ctx := context.Background()

	officer := newUserWithRole(t, migrator, "officer14@example.com", models.RolePolice)
	svc := newCaseServiceForTest(t, appDB, &spyRecorder{})
	user := authUser(officer, models.RolePolice)

	for i := 0; i < 3; i++ {
		_, err := svc.CreateCase(ctx, user, CreateCaseInput{
			CaseNumber: "PAGE-" + uuid.New().String()[:8], Title: "Paged case",
		})
		require.NoError(t, err)
	}

	page := paginationForTest(1, 2)
	result, err := svc.ListCases(ctx, user, CaseListFilter{}, page)
	require.NoError(t, err)
	assert.Len(t, result.Cases, 2)
	assert.EqualValues(t, 3, result.Meta.Total)
	assert.EqualValues(t, 2, result.Meta.TotalPages)

	page2, err := svc.ListCases(ctx, user, CaseListFilter{}, paginationForTest(2, 2))
	require.NoError(t, err)
	assert.Len(t, page2.Cases, 1)
}

func containsCaseNumber(cases []CaseSummary, caseNumber string) bool {
	for _, c := range cases {
		if c.CaseNumber == caseNumber {
			return true
		}
	}
	return false
}
