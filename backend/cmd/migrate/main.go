// Command migrate applies (or rolls back) Evidentia's PostgreSQL schema
// migrations. It connects using DATABASE_MIGRATOR_USER/PASSWORD — a
// privileged, schema-owning role distinct from the least-privilege
// evidentia_app role cmd/server connects as (see master prompt §41/§61)
// — never the running server's own credentials.
//
// Usage:
//
//	go run ./cmd/migrate up
//	go run ./cmd/migrate down
//	go run ./cmd/migrate version
package main

import (
	"database/sql"
	"errors"
	"fmt"
	"os"

	"github.com/golang-migrate/migrate/v4"
	pgx5migrate "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/joho/godotenv"

	"evidentia/backend/internal/config"
)

// migrationsPath is relative to the backend module root, matching how
// every other command in this repository (go run ./cmd/..., make targets)
// is invoked — from backend/.
const migrationsPath = "file://db/migrations"

func main() {
	_ = godotenv.Load()

	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: migrate <up|down|version>")
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
		return fmt.Errorf("unknown command %q (want up, down, or version)", cmd)
	}

	if err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return err
	}
	fmt.Println("migrate: " + cmd + " complete")
	return nil
}
