//go:build integration

package tests

import (
	"context"
	"database/sql"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	pgx5migrate "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/require"
)

// TestMigration_UpDownUpIsReproducible applies the real migration files
// (not a re-implementation of them) via the golang-migrate library, in a
// dedicated database created and dropped just for this test — never the
// shared "evidentia" database the other tests in this package use, so a
// failure here can't corrupt their fixtures.
func TestMigration_UpDownUpIsReproducible(t *testing.T) {
	ctx := context.Background()
	admin := migratorPool(t)

	const testDB = "evidentia_migration_test"
	_, err := admin.Exec(ctx, "DROP DATABASE IF EXISTS "+testDB)
	require.NoError(t, err)
	_, err = admin.Exec(ctx, "CREATE DATABASE "+testDB+" OWNER "+envOr("DATABASE_MIGRATOR_USER", "evidentia"))
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = admin.Exec(context.Background(), "DROP DATABASE IF EXISTS "+testDB)
	})

	dsn := "postgres://" + envOr("DATABASE_MIGRATOR_USER", "evidentia") +
		":" + envOr("DATABASE_MIGRATOR_PASSWORD", "changeme_example") +
		"@" + envOr("DATABASE_HOST", "localhost") + ":" + envOr("DATABASE_PORT", "5432") +
		"/" + testDB + "?sslmode=disable"

	newMigrator := func(t *testing.T) *migrate.Migrate {
		t.Helper()
		sqlDB, err := sql.Open("pgx", dsn)
		require.NoError(t, err)
		t.Cleanup(func() { _ = sqlDB.Close() })

		driver, err := pgx5migrate.WithInstance(sqlDB, &pgx5migrate.Config{})
		require.NoError(t, err)

		m, err := migrate.NewWithDatabaseInstance("file://../db/migrations", "evidentia_migration_test", driver)
		require.NoError(t, err)
		return m
	}

	assertTableCount := func(t *testing.T, want int) {
		t.Helper()
		pool, err := pgxpool.New(ctx, dsn)
		require.NoError(t, err)
		defer pool.Close()

		var count int
		require.NoError(t, pool.QueryRow(ctx,
			`SELECT count(*) FROM information_schema.tables WHERE table_schema = 'public'`,
		).Scan(&count))
		require.Equal(t, want, count)
	}

	// +1 for auth_sessions (System 3's 000002_auth_sessions migration —
	// not in expectedTables, which enumerates System 2's own core domain
	// tables specifically) and +1 for schema_migrations, golang-migrate's
	// own bookkeeping table — created on the first Up() and never touched
	// by either migration's down.
	withBookkeeping := len(expectedTables) + 2

	m := newMigrator(t)
	require.NoError(t, m.Up())
	assertTableCount(t, withBookkeeping)

	require.NoError(t, m.Down())
	assertTableCount(t, 1) // schema_migrations only

	// A fresh *migrate.Migrate against the same now-empty database — not
	// reusing the first instance — to prove "up" is reproducible from
	// scratch, not merely idempotent within one process's lifetime.
	m2 := newMigrator(t)
	require.NoError(t, m2.Up())
	assertTableCount(t, withBookkeeping)
}
