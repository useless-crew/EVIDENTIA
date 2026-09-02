//go:build integration

package tests

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var expectedTables = []string{
	"users", "roles", "permissions", "user_roles", "role_permissions",
	"cases", "case_members", "case_involved_parties",
	"documents", "redactions", "audit_log", "compliance_certificates",
}

func TestSchema_AllCoreTablesExist(t *testing.T) {
	pool := migratorPool(t)
	ctx := context.Background()

	for _, table := range expectedTables {
		var exists bool
		err := pool.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema='public' AND table_name=$1)`,
			table,
		).Scan(&exists)
		require.NoError(t, err)
		assert.True(t, exists, "table %q should exist", table)
	}
}

func TestSchema_ForeignKeysExist(t *testing.T) {
	pool := migratorPool(t)
	ctx := context.Background()

	cases := []struct {
		table, column, refTable string
	}{
		{"user_roles", "user_id", "users"},
		{"user_roles", "role_id", "roles"},
		{"role_permissions", "role_id", "roles"},
		{"role_permissions", "permission_id", "permissions"},
		{"cases", "created_by", "users"},
		{"case_members", "case_id", "cases"},
		{"case_members", "user_id", "users"},
		{"documents", "case_id", "cases"},
		{"documents", "uploaded_by", "users"},
		{"documents", "parent_document_id", "documents"},
		{"redactions", "source_document_id", "documents"},
		{"redactions", "result_document_id", "documents"},
		{"audit_log", "user_id", "users"},
		{"audit_log", "case_id", "cases"},
		{"compliance_certificates", "document_id", "documents"},
	}

	for _, c := range cases {
		var count int
		err := pool.QueryRow(ctx, `
			SELECT count(*)
			FROM information_schema.key_column_usage kcu
			JOIN information_schema.table_constraints tc
			  ON tc.constraint_name = kcu.constraint_name AND tc.constraint_type = 'FOREIGN KEY'
			JOIN information_schema.constraint_column_usage ccu
			  ON ccu.constraint_name = kcu.constraint_name
			WHERE kcu.table_name = $1 AND kcu.column_name = $2 AND ccu.table_name = $3`,
			c.table, c.column, c.refTable,
		).Scan(&count)
		require.NoError(t, err)
		assert.GreaterOrEqual(t, count, 1, "expected FK %s.%s -> %s.id", c.table, c.column, c.refTable)
	}
}

func TestSchema_RoleNameUniqueness(t *testing.T) {
	pool := migratorPool(t)
	ctx := context.Background()
	truncateAll(t, pool)

	_, err := pool.Exec(ctx, `INSERT INTO roles (name) VALUES ('DUPLICATE_ROLE')`)
	require.NoError(t, err)

	_, err = pool.Exec(ctx, `INSERT INTO roles (name) VALUES ('DUPLICATE_ROLE')`)
	require.Error(t, err)
}

func TestSchema_PermissionNameUniqueness(t *testing.T) {
	pool := migratorPool(t)
	ctx := context.Background()
	truncateAll(t, pool)

	_, err := pool.Exec(ctx, `INSERT INTO permissions (name, resource, action) VALUES ('dup:perm', 'x', 'y')`)
	require.NoError(t, err)

	_, err = pool.Exec(ctx, `INSERT INTO permissions (name, resource, action) VALUES ('dup:perm', 'x', 'y')`)
	require.Error(t, err)
}

func TestSchema_CaseNumberUniqueness(t *testing.T) {
	pool := migratorPool(t)
	ctx := context.Background()
	truncateAll(t, pool)

	userID := mustInsertUser(t, pool, "creator@example.com")

	_, err := pool.Exec(ctx, `INSERT INTO cases (case_number, title, created_by) VALUES ('DUP-1', 'A', $1)`, userID)
	require.NoError(t, err)

	_, err = pool.Exec(ctx, `INSERT INTO cases (case_number, title, created_by) VALUES ('DUP-1', 'B', $1)`, userID)
	require.Error(t, err)
}

func TestSchema_EmailUniquenessIsCaseInsensitive(t *testing.T) {
	pool := migratorPool(t)
	ctx := context.Background()
	truncateAll(t, pool)

	_, err := pool.Exec(ctx, `INSERT INTO users (email, password_hash, first_name, last_name) VALUES ('Same@Example.com', 'x', 'A', 'B')`)
	require.NoError(t, err)

	_, err = pool.Exec(ctx, `INSERT INTO users (email, password_hash, first_name, last_name) VALUES ('same@example.com', 'x', 'C', 'D')`)
	require.Error(t, err, "citext should make these collide despite differing case")
}

func TestSchema_DocumentHashMustBeExactly32Bytes(t *testing.T) {
	pool := migratorPool(t)
	ctx := context.Background()
	truncateAll(t, pool)

	userID := mustInsertUser(t, pool, "uploader@example.com")
	caseID := mustInsertCase(t, pool, "HASH-1", userID)

	_, err := pool.Exec(ctx, `
		INSERT INTO documents (case_id, document_type, filename, mime_type, file_size, sha256_hash, storage_bucket, storage_object_key, uploaded_by)
		VALUES ($1, 'OTHER', 'f.txt', 'text/plain', 10, decode('aabb', 'hex'), 'bucket', 'key1', $2)`,
		caseID, userID,
	)
	require.Error(t, err, "a 2-byte hash must be rejected")

	_, err = pool.Exec(ctx, `
		INSERT INTO documents (case_id, document_type, filename, mime_type, file_size, sha256_hash, storage_bucket, storage_object_key, uploaded_by)
		VALUES ($1, 'OTHER', 'f.txt', 'text/plain', 10, decode(repeat('ab', 32), 'hex'), 'bucket', 'key2', $2)`,
		caseID, userID,
	)
	assert.NoError(t, err, "a proper 32-byte hash must be accepted")
}

func TestSchema_DocumentFileSizeMustNotBeNegative(t *testing.T) {
	pool := migratorPool(t)
	ctx := context.Background()
	truncateAll(t, pool)

	userID := mustInsertUser(t, pool, "uploader2@example.com")
	caseID := mustInsertCase(t, pool, "SIZE-1", userID)

	_, err := pool.Exec(ctx, `
		INSERT INTO documents (case_id, document_type, filename, mime_type, file_size, sha256_hash, storage_bucket, storage_object_key, uploaded_by)
		VALUES ($1, 'OTHER', 'f.txt', 'text/plain', -1, decode(repeat('ab', 32), 'hex'), 'bucket', 'key3', $2)`,
		caseID, userID,
	)
	require.Error(t, err)
}

func TestSchema_RedactionResultDocumentMustBeUnique(t *testing.T) {
	pool := migratorPool(t)
	ctx := context.Background()
	truncateAll(t, pool)

	userID := mustInsertUser(t, pool, "redactor@example.com")
	caseID := mustInsertCase(t, pool, "REDACT-1", userID)
	source := mustInsertDocument(t, pool, caseID, userID, "key-source")
	result := mustInsertDocument(t, pool, caseID, userID, "key-result")
	other := mustInsertDocument(t, pool, caseID, userID, "key-other-source")

	_, err := pool.Exec(ctx, `INSERT INTO redactions (source_document_id, result_document_id, region_data, created_by) VALUES ($1, $2, '{}', $3)`, source, result, userID)
	require.NoError(t, err)

	// A second redaction cannot claim the SAME result document.
	_, err = pool.Exec(ctx, `INSERT INTO redactions (source_document_id, result_document_id, region_data, created_by) VALUES ($1, $2, '{}', $3)`, other, result, userID)
	require.Error(t, err)
}

func TestSchema_CaseMemberActiveUniqueness(t *testing.T) {
	pool := migratorPool(t)
	ctx := context.Background()
	truncateAll(t, pool)

	userID := mustInsertUser(t, pool, "member@example.com")
	caseID := mustInsertCase(t, pool, "MEMBER-1", userID)

	_, err := pool.Exec(ctx, `INSERT INTO case_members (case_id, user_id, membership_type, added_by) VALUES ($1, $2, 'OWNER', $2)`, caseID, userID)
	require.NoError(t, err)

	// A second ACTIVE membership row for the same (case, user) is rejected...
	_, err = pool.Exec(ctx, `INSERT INTO case_members (case_id, user_id, membership_type, added_by) VALUES ($1, $2, 'VIEWER', $2)`, caseID, userID)
	require.Error(t, err)

	// ...but a historical (removed) row for the same pair is fine.
	_, err = pool.Exec(ctx, `UPDATE case_members SET removed_at = now() WHERE case_id = $1 AND user_id = $2`, caseID, userID)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO case_members (case_id, user_id, membership_type, added_by) VALUES ($1, $2, 'VIEWER', $2)`, caseID, userID)
	assert.NoError(t, err)
}

// ---- shared fixture helpers (used across this package's test files) ----

func mustInsertUser(t *testing.T, pool *pgxpool.Pool, email string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := pool.QueryRow(context.Background(),
		`INSERT INTO users (email, password_hash, first_name, last_name) VALUES ($1, 'x', 'A', 'B') RETURNING id`,
		email,
	).Scan(&id)
	require.NoError(t, err)
	return id
}

func mustInsertCase(t *testing.T, pool *pgxpool.Pool, caseNumber string, createdBy uuid.UUID) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := pool.QueryRow(context.Background(),
		`INSERT INTO cases (case_number, title, created_by) VALUES ($1, $1, $2) RETURNING id`,
		caseNumber, createdBy,
	).Scan(&id)
	require.NoError(t, err)
	return id
}

func mustInsertDocument(t *testing.T, pool *pgxpool.Pool, caseID, uploadedBy uuid.UUID, objectKey string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := pool.QueryRow(context.Background(), `
		INSERT INTO documents (case_id, document_type, filename, mime_type, file_size, sha256_hash, storage_bucket, storage_object_key, uploaded_by)
		VALUES ($1, 'OTHER', 'f.txt', 'text/plain', 10, decode(repeat('ab', 32), 'hex'), 'bucket', $2, $3)
		RETURNING id`,
		caseID, objectKey, uploadedBy,
	).Scan(&id)
	require.NoError(t, err)
	return id
}
