// Command migrate applies (or rolls back) Evidentia's PostgreSQL schema
// migrations, and (via the `seed` subcommand — System 17) applies the
// fixed reference-data seed (roles/permissions/role_permissions). It
// connects using DATABASE_MIGRATOR_USER/PASSWORD — a privileged,
// schema-owning role distinct from the least-privilege evidentia_app
// role cmd/server connects as (see master prompt §41/§61) — never the
// running server's own credentials.
//
// `seed` is deliberately folded into this same binary rather than kept
// as the separate backend/scripts/seed_db.sh shell script it started as:
// that script needs `psql` on PATH and is invoked from the `backend/`
// directory, neither of which docker-compose.prod.yml's minimal runtime
// image provides/assumes — reusing the connection this binary already
// knows how to open avoids a second, container-unfriendly mechanism for
// what is fundamentally the same "get this database into its expected
// starting state" job as `up`. scripts/seed_db.sh remains for local
// host-based development (`make seed`); both read the exact same
// db/seed/*.sql files, so there is no risk of the two drifting apart.
//
// Usage:
//
//	go run ./cmd/migrate up
//	go run ./cmd/migrate down
//	go run ./cmd/migrate version
//	go run ./cmd/migrate seed
package main

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/golang-migrate/migrate/v4"
	pgx5migrate "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/joho/godotenv"

	"evidentia/backend/internal/config"
)

// migrationsPath/seedPath are relative to the backend module root,
// matching how every other command in this repository (go run ./cmd/...,
// make targets) is invoked — from backend/.
const migrationsPath = "file://db/migrations"
const seedDir = "db/seed"

func main() {
	_ = godotenv.Load()

	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: migrate <up|down|version|seed>")
		os.Exit(1)
	}

	if err := run(os.Args[1]); err != nil {
		fmt.Fprintln(os.Stderr, "migrate: "+err.Error())
		os.Exit(1)
	}
}

func run(cmd string) error {
	cfg, err := config.LoadMigrator()
	if err != nil {
		return fmt.Errorf("load migrator config: %w", err)
	}

	sqlDB, err := sql.Open("pgx", cfg.DSN())
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer sqlDB.Close()

	if cmd == "seed" {
		return runSeed(sqlDB)
	}

	driver, err := pgx5migrate.WithInstance(sqlDB, &pgx5migrate.Config{})
	if err != nil {
		return fmt.Errorf("init migration driver: %w", err)
	}

	m, err := migrate.NewWithDatabaseInstance(migrationsPath, "evidentia", driver)
	if err != nil {
		return fmt.Errorf("init migrator: %w", err)
	}

	switch cmd {
	case "up":
		err = m.Up()
	case "down":
		err = m.Down()
	case "version":
		version, dirty, vErr := m.Version()
		if vErr != nil {
			return fmt.Errorf("read version: %w", vErr)
		}
		fmt.Printf("version=%d dirty=%t\n", version, dirty)
		return nil
	default:
		return fmt.Errorf("unknown command %q (want up, down, version, or seed)", cmd)
	}

	if err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return err
	}
	fmt.Println("migrate: " + cmd + " complete")
	return nil
}

// runSeed applies every db/seed/*.sql file, in filename order, over the
// already-open migrator connection. Each file executes as one
// sql.DB.Exec call: pgx's stdlib driver sends a parameterless Exec via
// Postgres's simple query protocol, which (unlike a prepared statement)
// natively supports multiple ;-separated statements in one file — the
// same multi-statement shape backend/scripts/seed_db.sh's psql -f
// already relies on. Every seed file is idempotent (ON CONFLICT DO
// NOTHING — see db/seed/001_reference_data.sql), so re-running this is
// always safe.
func runSeed(sqlDB *sql.DB) error {
	pattern := filepath.Join(seedDir, "*.sql")
	files, err := filepath.Glob(pattern)
	if err != nil {
		return fmt.Errorf("list seed files (%s): %w", pattern, err)
	}
	if len(files) == 0 {
		return fmt.Errorf("no seed files found matching %s", pattern)
	}
	sort.Strings(files)

	for _, f := range files {
		contents, err := os.ReadFile(f)
		if err != nil {
			return fmt.Errorf("read %s: %w", f, err)
		}
		fmt.Println("migrate: seeding " + f)
		if _, err := sqlDB.Exec(string(contents)); err != nil {
			return fmt.Errorf("apply %s: %w", f, err)
		}
	}

	fmt.Println("migrate: seed complete")
	return nil
}
