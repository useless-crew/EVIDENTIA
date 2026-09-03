//go:build integration

// System 5 RLS-scoping tests for FORENSICS/JUDGE case_members
// relationships (see db_rls_test.go for the LAWYER/ADMIN/transaction-
// locality/bootstrap cases already covered there — this file adds the
// remaining role-relationship coverage master prompt §43 asks for,
// without duplicating what db_rls_test.go already proves). See
// helpers_test.go for migratorPool/appPool/truncateAll and
// db_schema_test.go for mustInsertUser/mustInsertCase.
package tests

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"evidentia/backend/db/generated"
	"evidentia/backend/internal/models"
	"evidentia/backend/internal/repository"
)

// addCaseMember is defined in abac_test.go (same package) — reused here
// rather than duplicated.

func TestRLS_ForensicsSeesOnlyLinkedCase(t *testing.T) {
	migrator := migratorPool(t)
	truncateAll(t, migrator)
	app := appPool(t)

	officer := mustInsertUser(t, migrator, "officer-forensics-a@example.com")
	forensics := mustInsertUser(t, migrator, "forensics-a@example.com")

	caseA := mustInsertCase(t, migrator, "FORENSICS-A", officer)
	mustInsertCase(t, migrator, "FORENSICS-B", officer)
	addCaseMember(t, migrator, caseA, forensics, officer, models.MembershipTypeForensics)

	visible := casesVisibleTo(t, app, repository.AppIdentity{UserID: forensics, Role: models.RoleForensics})
	assert.ElementsMatch(t, []string{"FORENSICS-A"}, visible, "a forensics user linked to case A must not see unrelated case B")
	assert.NotContains(t, visible, "FORENSICS-B")
}

func TestRLS_JudgeSeesOnlyAssignedCase(t *testing.T) {
	// No docket table exists in the current schema (see docs/SECURITY.md's
	// "Case-based ABAC" — deferred docket-specific enforcement is
	// documented there, not invented here). The safest supported scope is
	// the same case_members mechanism every other non-admin role uses.
	migrator := migratorPool(t)
	truncateAll(t, migrator)
	app := appPool(t)

	officer := mustInsertUser(t, migrator, "officer-judge-a@example.com")
	judge := mustInsertUser(t, migrator, "judge-a@example.com")

	caseA := mustInsertCase(t, migrator, "JUDGE-A", officer)
	mustInsertCase(t, migrator, "JUDGE-B", officer)
	addCaseMember(t, migrator, caseA, judge, officer, models.MembershipTypeJudge)

	visible := casesVisibleTo(t, app, repository.AppIdentity{UserID: judge, Role: models.RoleJudge})
	assert.ElementsMatch(t, []string{"JUDGE-A"}, visible)
	assert.NotContains(t, visible, "JUDGE-B")
}

func TestRLS_RemovedMembershipRevokesCaseVisibility(t *testing.T) {
	migrator := migratorPool(t)
	truncateAll(t, migrator)
	app := appPool(t)
	ctx := context.Background()

	officer := mustInsertUser(t, migrator, "officer-removed@example.com")
	lawyer := mustInsertUser(t, migrator, "lawyer-removed@example.com")
	caseA := mustInsertCase(t, migrator, "REMOVED-1", officer)
	addCaseMember(t, migrator, caseA, lawyer, officer, models.MembershipTypeLawyer)

	visibleBefore := casesVisibleTo(t, app, repository.AppIdentity{UserID: lawyer, Role: models.RoleLawyer})
	assert.Contains(t, visibleBefore, "REMOVED-1")

	_, err := migrator.Exec(ctx, `UPDATE case_members SET removed_at = now() WHERE case_id = $1 AND user_id = $2`, caseA, lawyer)
	require.NoError(t, err)

	visibleAfter := casesVisibleTo(t, app, repository.AppIdentity{UserID: lawyer, Role: models.RoleLawyer})
	assert.NotContains(t, visibleAfter, "REMOVED-1", "a removed (soft-deleted) membership must revoke visibility")
}

func TestRLS_ListCasesFiltered_ScopesLikePlainListCases(t *testing.T) {
	// service.CaseService.ListCases layers filter/pagination on top of
	// ListCasesFiltered — this test proves that query is scoped by RLS
	// exactly like the plain ListCases query db_rls_test.go already
	// exercises (same policy, different query), so a filtered/paginated
	// request can never leak a row plain listing wouldn't already show.
	migrator := migratorPool(t)
	truncateAll(t, migrator)
	app := appPool(t)
	ctx := context.Background()

	userA := mustInsertUser(t, migrator, "filtered-a@example.com")
	userB := mustInsertUser(t, migrator, "filtered-b@example.com")
	mustInsertCase(t, migrator, "FILTERED-A", userA)
	mustInsertCase(t, migrator, "FILTERED-B", userB)

	var rows []generated.Case
	err := repository.WithTx(ctx, app, repository.AppIdentity{UserID: userA, Role: models.RoleLawyer}, func(ctx context.Context, q *generated.Queries) error {
		r, err := q.ListCasesFiltered(ctx, generated.ListCasesFilteredParams{LimitVal: 100, OffsetVal: 0})
		rows = r
		return err
	})
	require.NoError(t, err)

	var numbers []string
	for _, c := range rows {
		numbers = append(numbers, c.CaseNumber)
	}
	assert.Contains(t, numbers, "FILTERED-A")
	assert.NotContains(t, numbers, "FILTERED-B", "RLS must scope ListCasesFiltered identically to ListCases")
}

func TestRLS_CountCasesFiltered_MatchesVisibleRowCount(t *testing.T) {
	migrator := migratorPool(t)
	truncateAll(t, migrator)
	app := appPool(t)
	ctx := context.Background()

	userA := mustInsertUser(t, migrator, "count-a@example.com")
	userB := mustInsertUser(t, migrator, "count-b@example.com")
	mustInsertCase(t, migrator, "COUNT-A1", userA)
	mustInsertCase(t, migrator, "COUNT-A2", userA)
	mustInsertCase(t, migrator, "COUNT-B1", userB)

	var total int64
	err := repository.WithTx(ctx, app, repository.AppIdentity{UserID: userA, Role: models.RolePolice}, func(ctx context.Context, q *generated.Queries) error {
		n, err := q.CountCasesFiltered(ctx, generated.CountCasesFilteredParams{})
		total = n
		return err
	})
	require.NoError(t, err)
	assert.EqualValues(t, 2, total, "count must reflect only RLS-visible rows, never the full table")
}
