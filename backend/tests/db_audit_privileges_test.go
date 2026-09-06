//go:build integration

package tests

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"evidentia/backend/db/generated"
	"evidentia/backend/internal/models"
	"evidentia/backend/internal/repository"
)

// TestAuditPrivileges_RuntimeRoleCannotUpdateOrDelete is the single most
// important test in this package: master prompt §26/§42 make audit-log
// immutability a hard, database-enforced requirement, not merely an
// application-code convention.
func TestAuditPrivileges_RuntimeRoleCannotUpdateOrDelete(t *testing.T) {
	migrator := migratorPool(t)
	truncateAll(t, migrator)
	app := appPool(t)
	ctx := context.Background()

	userID := mustInsertUser(t, migrator, "auditor@example.com")

	// Insert one entry as evidentia_app (allowed) so there's a row to
	// attempt to tamper with.
	err := repository.WithTx(ctx, app, repository.AppIdentity{UserID: userID, Role: models.RoleAdmin}, func(ctx context.Context, q *generated.Queries) error {
		_, err := q.InsertAuditEntry(ctx, generated.InsertAuditEntryParams{
			ID:           uuid.New(),
			Timestamp:    time.Now().UTC(),
			UserID:       &userID,
			Action:       "case.create",
			ResourceType: "case",
			Metadata:     []byte(`{}`),
			Hash:         mustHashBytes(t, 0xAA),
		})
		return err
	})
	require.NoError(t, err, "INSERT on audit_log must be allowed for evidentia_app")

	_, err = app.Exec(ctx, `UPDATE audit_log SET action = 'tampered'`)
	require.Error(t, err, "UPDATE on audit_log must be denied for evidentia_app")
	assert.Contains(t, err.Error(), "permission denied")

	_, err = app.Exec(ctx, `DELETE FROM audit_log`)
	require.Error(t, err, "DELETE on audit_log must be denied for evidentia_app")
	assert.Contains(t, err.Error(), "permission denied")

	var count int
	require.NoError(t, migrator.QueryRow(ctx, `SELECT count(*) FROM audit_log`).Scan(&count))
	assert.Equal(t, 1, count, "the entry must still be exactly as inserted — no tampering succeeded")
}

func TestAuditPrivileges_RuntimeRoleDoesNotOwnAuditLog(t *testing.T) {
	migrator := migratorPool(t)
	ctx := context.Background()

	var owner string
	err := migrator.QueryRow(ctx, `
		SELECT pg_catalog.pg_get_userbyid(c.relowner)
		FROM pg_catalog.pg_class c
		WHERE c.relname = 'audit_log'`,
	).Scan(&owner)
	require.NoError(t, err)
	assert.NotEqual(t, "evidentia_app", owner, "evidentia_app must not own audit_log — an owner can ALTER/DROP it regardless of GRANTs")
}

func TestAuditPrivileges_RuntimeRoleIsNotSuperuserAndDoesNotBypassRLS(t *testing.T) {
	migrator := migratorPool(t)
	ctx := context.Background()

	var isSuper, bypassRLS bool
	err := migrator.QueryRow(ctx,
		`SELECT rolsuper, rolbypassrls FROM pg_catalog.pg_roles WHERE rolname = 'evidentia_app'`,
	).Scan(&isSuper, &bypassRLS)
	require.NoError(t, err)
	assert.False(t, isSuper, "evidentia_app must not be a Postgres superuser")
	assert.False(t, bypassRLS, "evidentia_app must not have BYPASSRLS")
}

func TestAuditPrivileges_RuntimeRoleCanSelectAndInsertOnly(t *testing.T) {
	migrator := migratorPool(t)
	ctx := context.Background()

	rows, err := migrator.Query(ctx, `
		SELECT privilege_type
		FROM information_schema.role_table_grants
		WHERE table_name = 'audit_log' AND grantee = 'evidentia_app'
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

	assert.ElementsMatch(t, []string{"SELECT", "INSERT"}, privileges,
		"evidentia_app must hold exactly SELECT and INSERT on audit_log — no UPDATE, no DELETE, no TRUNCATE")
}

func TestAuditPrivileges_GenesisEntryMustBeUnique(t *testing.T) {
	migrator := migratorPool(t)
	truncateAll(t, migrator)
	app := appPool(t)
	ctx := context.Background()

	userID := mustInsertUser(t, migrator, "genesis@example.com")
	ident := repository.AppIdentity{UserID: userID, Role: models.RoleAdmin}

	insertGenesis := func() error {
		return repository.WithTx(ctx, app, ident, func(ctx context.Context, q *generated.Queries) error {
			_, err := q.InsertAuditEntry(ctx, generated.InsertAuditEntryParams{
				ID:           uuid.New(),
				Timestamp:    time.Now().UTC(),
				UserID:       &userID,
				Action:       "genesis.attempt",
				ResourceType: "system",
				Metadata:     []byte(`{}`),
				Hash:         mustHashBytes(t, 0xBB),
				// PrevHash left nil: this is a genesis-shaped entry.
			})
			return err
		})
	}

	require.NoError(t, insertGenesis(), "the first genesis entry must be accepted")
	err := insertGenesis()
	require.Error(t, err, "a second entry with a NULL prev_hash must be rejected — at most one genesis entry")
}

func TestAuditPrivileges_OnePredecessorPerEntry(t *testing.T) {
	migrator := migratorPool(t)
	truncateAll(t, migrator)
	app := appPool(t)
	ctx := context.Background()

	userID := mustInsertUser(t, migrator, "chain@example.com")
	ident := repository.AppIdentity{UserID: userID, Role: models.RoleAdmin}
	prev := mustHashBytes(t, 0x11)

	insertChild := func(hashByte byte) error {
		return repository.WithTx(ctx, app, ident, func(ctx context.Context, q *generated.Queries) error {
			_, err := q.InsertAuditEntry(ctx, generated.InsertAuditEntryParams{
				ID:           uuid.New(),
				Timestamp:    time.Now().UTC(),
				UserID:       &userID,
				Action:       "case.create",
				ResourceType: "case",
				Metadata:     []byte(`{}`),
				PrevHash:     prev,
				Hash:         mustHashBytes(t, hashByte),
			})
			return err
		})
	}

	require.NoError(t, insertChild(0x22), "the first entry claiming this predecessor must be accepted")
	err := insertChild(0x33)
	require.Error(t, err, "a second entry claiming the SAME predecessor must be rejected — one canonical successor per entry")
}

// mustHashBytes returns a 32-byte slice filled with fill, satisfying the
// hash-length CHECK constraints without depending on any real SHA-256
// computation (that belongs to a later system).
func mustHashBytes(t *testing.T, fill byte) []byte {
	t.Helper()
	b := make([]byte, 32)
	for i := range b {
		b[i] = fill
	}
	return b
}
