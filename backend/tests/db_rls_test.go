//go:build integration

package tests

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"evidentia/backend/db/generated"
	"evidentia/backend/internal/models"
	"evidentia/backend/internal/repository"
)

// fixtureSet holds IDs shared across the RLS tests in this file. Both
// cases are seeded via the migrator pool (bypassing RLS/app-role grants
// entirely), so these tests exercise only READ visibility and the
// specific write policies under test — not every INSERT policy.
type fixtureSet struct {
	userA, userB, caseA, caseB uuid.UUID
}

func seedTwoUsersTwoCases(t *testing.T, migrator *pgxpool.Pool) fixtureSet {
	t.Helper()
	ctx := context.Background()

	userA := mustInsertUser(t, migrator, "usera@example.com")
	userB := mustInsertUser(t, migrator, "userb@example.com")

	var caseA, caseB uuid.UUID
	require.NoError(t, migrator.QueryRow(ctx,
		`INSERT INTO cases (case_number, title, created_by) VALUES ('CASE-A', 'Case A', $1) RETURNING id`, userA,
	).Scan(&caseA))
	require.NoError(t, migrator.QueryRow(ctx,
		`INSERT INTO cases (case_number, title, created_by) VALUES ('CASE-B', 'Case B', $1) RETURNING id`, userB,
	).Scan(&caseB))

	_, err := migrator.Exec(ctx, `INSERT INTO case_members (case_id, user_id, membership_type, added_by) VALUES ($1, $2, 'OWNER', $2)`, caseA, userA)
	require.NoError(t, err)
	_, err = migrator.Exec(ctx, `INSERT INTO case_members (case_id, user_id, membership_type, added_by) VALUES ($1, $2, 'OWNER', $2)`, caseB, userB)
	require.NoError(t, err)

	return fixtureSet{userA: userA, userB: userB, caseA: caseA, caseB: caseB}
}

// casesVisibleTo runs ListCases as ident and returns the case_numbers it
// can see — the central tool these RLS tests use to observe row
// visibility through the real production code path (repository.WithTx),
// not a hand-rolled reimplementation of it.
func casesVisibleTo(t *testing.T, pool *pgxpool.Pool, ident repository.AppIdentity) []string {
	t.Helper()
	var numbers []string
	err := repository.WithTx(context.Background(), pool, ident, func(ctx context.Context, q *generated.Queries) error {
		rows, err := q.ListCases(ctx, generated.ListCasesParams{Limit: 100, Offset: 0})
		if err != nil {
			return err
		}
		for _, c := range rows {
			numbers = append(numbers, c.CaseNumber)
		}
		return nil
	})
	require.NoError(t, err)
	return numbers
}

func TestRLS_FailsClosedWithoutIdentity(t *testing.T) {
	migrator := migratorPool(t)
	truncateAll(t, migrator)
	seedTwoUsersTwoCases(t, migrator)

	app := appPool(t)
	ctx := context.Background()

	// No app.user_id / app.role ever set on this connection — the raw
	// query path (not repository.WithTx), to prove the database itself,
	// not just the Go wrapper, denies access by default.
	var count int
	require.NoError(t, app.QueryRow(ctx, `SELECT count(*) FROM cases`).Scan(&count))
	assert.Equal(t, 0, count, "no identity must mean no visible rows, not unrestricted access")

	require.NoError(t, app.QueryRow(ctx, `SELECT count(*) FROM documents`).Scan(&count))
	assert.Equal(t, 0, count)

	require.NoError(t, app.QueryRow(ctx, `SELECT count(*) FROM audit_log`).Scan(&count))
	assert.Equal(t, 0, count)

	require.NoError(t, app.QueryRow(ctx, `SELECT count(*) FROM audit_verifications`).Scan(&count))
	assert.Equal(t, 0, count, "audit_verifications_select requires current_app_role() = 'ADMIN' — no identity must mean zero visible rows, exactly like every other RLS-protected table")
}

func TestRLS_UserSeesOnlyOwnCase(t *testing.T) {
	migrator := migratorPool(t)
	truncateAll(t, migrator)
	fx := seedTwoUsersTwoCases(t, migrator)
	app := appPool(t)

	assert.ElementsMatch(t, []string{"CASE-A"}, casesVisibleTo(t, app, repository.AppIdentity{UserID: fx.userA, Role: models.RoleLawyer}))
	assert.ElementsMatch(t, []string{"CASE-B"}, casesVisibleTo(t, app, repository.AppIdentity{UserID: fx.userB, Role: models.RoleLawyer}))
}

func TestRLS_LawyerCannotAccessUnrelatedCase(t *testing.T) {
	// Same scenario, framed explicitly per master prompt §91's security
	// review checklist ("Can a lawyer access an unrelated case?").
	migrator := migratorPool(t)
	truncateAll(t, migrator)
	fx := seedTwoUsersTwoCases(t, migrator)
	app := appPool(t)

	visible := casesVisibleTo(t, app, repository.AppIdentity{UserID: fx.userA, Role: models.RoleLawyer})
	assert.NotContains(t, visible, "CASE-B")
}

func TestRLS_AdminSeesAllCases(t *testing.T) {
	migrator := migratorPool(t)
	truncateAll(t, migrator)
	fx := seedTwoUsersTwoCases(t, migrator)
	app := appPool(t)

	visible := casesVisibleTo(t, app, repository.AppIdentity{UserID: fx.userB, Role: models.RoleAdmin})
	assert.ElementsMatch(t, []string{"CASE-A", "CASE-B"}, visible)
}

func TestRLS_TransactionLocalIdentityDoesNotLeak(t *testing.T) {
	// The single most important property for a pooled-connection
	// application: set_config(..., true) must never survive past the
	// transaction that set it, even on the SAME underlying connection.
	migrator := migratorPool(t)
	truncateAll(t, migrator)
	fx := seedTwoUsersTwoCases(t, migrator)
	ctx := context.Background()

	// Force both transactions onto the same physical connection by using
	// a pool capped at one connection.
	onePool, err := pgxpool.New(ctx, appDSN()+"&pool_max_conns=1")
	require.NoError(t, err)
	defer onePool.Close()

	visible := casesVisibleTo(t, onePool, repository.AppIdentity{UserID: fx.userA, Role: models.RoleAdmin})
	assert.Len(t, visible, 2, "admin identity set for this transaction should see both cases")

	// A second, independent transaction on the same pool (and therefore,
	// with pool_max_conns=1, the same connection) with NO identity set.
	var count int
	require.NoError(t, onePool.QueryRow(ctx, `SELECT count(*) FROM cases`).Scan(&count))
	assert.Equal(t, 0, count, "the previous transaction's identity must not have survived on the reused connection")
}

func TestRLS_CaseCreatorCanBootstrapOwnMembership(t *testing.T) {
	// Regression test for the bootstrap deadlock found and fixed while
	// developing this migration: a case's creator must be able to see
	// (and therefore insert the first case_members row for) their own
	// brand-new case, before any case_members row exists for it.
	migrator := migratorPool(t)
	truncateAll(t, migrator)
	app := appPool(t)
	ctx := context.Background()

	userID := mustInsertUser(t, migrator, "creator2@example.com")

	err := repository.WithTx(ctx, app, repository.AppIdentity{UserID: userID, Role: models.RoleLawyer}, func(ctx context.Context, q *generated.Queries) error {
		c, err := q.CreateCase(ctx, generated.CreateCaseParams{
			CaseNumber: "BOOTSTRAP-1",
			Title:      "Bootstrap",
			CreatedBy:  userID,
			Metadata:   []byte(`{}`),
		})
		if err != nil {
			return err
		}
		_, err = q.AddCaseMember(ctx, generated.AddCaseMemberParams{
			CaseID:         c.ID,
			UserID:         userID,
			MembershipType: models.MembershipTypeOwner,
			AddedBy:        userID,
		})
		return err
	})
	require.NoError(t, err)
}
