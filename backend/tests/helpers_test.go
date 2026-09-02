//go:build integration

// Package tests holds System 2's database integration tests — schema,
// Row-Level Security, and audit-log privilege behavior — run with:
//
//	go test -tags=integration ./tests/...
//
// Requires the docker-compose postgres service up, with the migration
// already applied (go run ./cmd/migrate up) and evidentia_app's password
// left at the migration's default ('changeme_example' — see
// 000001_init_schema.up.sql). Existing placeholder files in this
// directory (auth_test.go, hash_test.go, rbac_test.go, abac_test.go,
// document_test.go) belong to later systems and are deliberately left
// untouched — nothing here duplicates or implements their scope.
package tests

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// migratorDSN connects as the privileged, schema-owning role (matching
// docker-compose's Postgres bootstrap superuser by default) — used only
// to set up/tear down fixtures, never to exercise RLS itself.
func migratorDSN() string {
	return "postgres://" + envOr("DATABASE_MIGRATOR_USER", "evidentia") +
		":" + envOr("DATABASE_MIGRATOR_PASSWORD", "changeme_example") +
		"@" + envOr("DATABASE_HOST", "localhost") + ":" + envOr("DATABASE_PORT", "5432") +
		"/" + envOr("DATABASE_NAME", "evidentia") + "?sslmode=disable"
}

// appDSN connects as evidentia_app, the least-privilege, RLS-bound role
// the running server itself uses — every RLS/privilege test in this
// package connects this way, never as the migrator.
func appDSN() string {
	return "postgres://evidentia_app:" + envOr("DATABASE_APP_PASSWORD", "changeme_example") +
		"@" + envOr("DATABASE_HOST", "localhost") + ":" + envOr("DATABASE_PORT", "5432") +
		"/" + envOr("DATABASE_NAME", "evidentia") + "?sslmode=disable"
}

func newPool(t *testing.T, dsn string) *pgxpool.Pool {
	t.Helper()
	pool, err := pgxpool.New(context.Background(), dsn)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	require.NoError(t, pool.Ping(context.Background()))
	return pool
}

func migratorPool(t *testing.T) *pgxpool.Pool { return newPool(t, migratorDSN()) }
func appPool(t *testing.T) *pgxpool.Pool      { return newPool(t, appDSN()) }

// truncateAll clears every application table (as the migrator, which owns
// them) between tests, so fixtures in one test can never leak into
// another. schema_migrations (golang-migrate's own bookkeeping table) is
// deliberately left alone.
func truncateAll(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	_, err := pool.Exec(context.Background(), `
		TRUNCATE TABLE
			compliance_certificates, audit_log, redactions, documents,
			case_involved_parties, case_members, cases,
			role_permissions, user_roles, permissions, roles, users
		RESTART IDENTITY CASCADE`)
	require.NoError(t, err)
}
