//go:build integration

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

// TestAuditVerificationPrivileges_RuntimeRoleCanSelectInsertUpdateNotDelete
// is System 11's counterpart to db_audit_privileges_test.go's audit_log
// checks — audit_verifications is a job-status table (mutated by design,
// unlike the append-only audit chain itself), so evidentia_app legitimately
// holds UPDATE here where it holds none on audit_log. DELETE remains
// denied either way: a verification run is a permanent record once
// created.
func TestAuditVerificationPrivileges_RuntimeRoleCanSelectInsertUpdateNotDelete(t *testing.T) {
	migrator := migratorPool(t)
	ctx := context.Background()

	rows, err := migrator.Query(ctx, `
		SELECT privilege_type
		FROM information_schema.role_table_grants
		WHERE table_name = 'audit_verifications' AND grantee = 'evidentia_app'
		ORDER BY privilege_type`,
	)
	require.NoError(t, err)
	defer rows.Close()

	var privileges []string
	for rows.Next() {
		var p string
		require.NoError(t, rows.Scan(&p))
		privileges = append(privileges, p)
	}
	require.NoError(t, rows.Err())

	assert.ElementsMatch(t, []string{"SELECT", "INSERT", "UPDATE"}, privileges,
		"evidentia_app must hold SELECT, INSERT, and UPDATE on audit_verifications — but never DELETE")
}

// TestAuditVerificationPrivileges_OnlyAdminIdentityCanReadOrWrite exercises
// audit_verifications_select/_insert/_update's actual RLS condition
// (current_app_role() = 'ADMIN', nothing else) through the real
// application transaction path (repository.WithTx), not a raw SQL
// assertion about grants alone.
func TestAuditVerificationPrivileges_OnlyAdminIdentityCanReadOrWrite(t *testing.T) {
	migrator := migratorPool(t)
	truncateAll(t, migrator)
	app := appPool(t)
	ctx := context.Background()

	adminUser := mustInsertUser(t, migrator, "auditverif-admin@example.com")
	nonAdminUser := mustInsertUser(t, migrator, "auditverif-police@example.com")

	var created generated.AuditVerification
	err := repository.WithTx(ctx, app, repository.AppIdentity{UserID: adminUser, Role: models.RoleAdmin}, func(ctx context.Context, q *generated.Queries) error {
		c, err := q.CreateAuditVerification(ctx, generated.CreateAuditVerificationParams{RequestedByUserID: adminUser})
		created = c
		return err
	})
	require.NoError(t, err, "an ADMIN identity must be able to INSERT into audit_verifications")

	// A non-ADMIN identity sees zero rows — RLS, not a 403 from
	// application code, is what actually enforces this at the database
	// layer.
	var count int
	err = repository.WithTx(ctx, app, repository.AppIdentity{UserID: nonAdminUser, Role: models.RolePolice}, func(ctx context.Context, q *generated.Queries) error {
		rows, err := q.ListAuditVerificationsFiltered(ctx, generated.ListAuditVerificationsFilteredParams{LimitVal: 100})
		count = len(rows)
		return err
	})
	require.NoError(t, err)
	assert.Equal(t, 0, count, "audit_verifications_select requires ADMIN — a non-admin identity must see zero rows, never another role's verification history")

	// A non-ADMIN identity cannot INSERT either.
	err = repository.WithTx(ctx, app, repository.AppIdentity{UserID: nonAdminUser, Role: models.RolePolice}, func(ctx context.Context, q *generated.Queries) error {
		_, err := q.CreateAuditVerification(ctx, generated.CreateAuditVerificationParams{RequestedByUserID: nonAdminUser})
		return err
	})
	require.Error(t, err, "audit_verifications_insert requires ADMIN")

	// Direct DELETE is denied outright (no policy, no grant) even for an
	// otherwise-ADMIN identity — the grant, not just RLS, is what blocks
	// this.
	_, err = app.Exec(ctx, `DELETE FROM audit_verifications WHERE id = $1`, created.ID)
	require.Error(t, err, "DELETE on audit_verifications must be denied for evidentia_app regardless of identity")
	assert.Contains(t, err.Error(), "permission denied")
}
