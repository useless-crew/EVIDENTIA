// Command devuser creates (or resets the password/role of) a single
// local-development login account, directly in the users/user_roles
// tables — the same tables System 3's real login flow reads.
//
// This exists because no user-registration/admin-user-creation endpoint
// is implemented yet (internal/handlers/user remains a TODO stub — see
// ARCHITECTURE.md), and backend/db/seed/001_reference_data.sql
// deliberately seeds no user rows ("a seeded user would need a real
// bcrypt password_hash... it must be created through [the registration]
// system's flow, or an explicit, separate, environment-variable-driven
// script, never hardcoded [in the committed seed file]"). This is that
// script: every credential is supplied by the caller at run time (a flag
// or an env var), nothing is hardcoded or committed, and it is never
// wired into any HTTP route — it exists purely so a developer or the
// frontend integration's own end-to-end test can log in against a real
// backend without a production user-management system.
//
// Connects using DATABASE_MIGRATOR_USER/PASSWORD (see cmd/migrate) —
// the users/user_roles grants would work under the runtime evidentia_app
// role too, but this reuses the same privileged-role convention every
// other one-off administrative command in this repository already uses.
//
// Usage:
//
//	go run ./cmd/devuser -email=officer@example.test -password=... -first=Jane -last=Doe -role=POLICE
//
// Re-running with the same email updates the password hash, name, and
// status (so a forgotten dev password can just be reset), and adds the
// role assignment if it's missing — it never removes an existing role.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"

	"evidentia/backend/internal/auth"
	"evidentia/backend/internal/config"
)

// validRoles mirrors the fixed catalog db/seed/001_reference_data.sql
// seeds into the roles table — not re-derived from the database up
// front, so a typo is rejected immediately with a clear message instead
// of a opaque foreign-key error after the user row is already written.
var validRoles = map[string]bool{
	"ADMIN": true, "POLICE": true, "FORENSICS": true, "LAWYER": true, "JUDGE": true,
}

func main() {
	_ = godotenv.Load()

	email := flag.String("email", "", "login email (required)")
	password := flag.String("password", "", "plaintext password to hash and store (required, min 8 characters)")
	first := flag.String("first", "", "first name (required)")
	last := flag.String("last", "", "last name (required)")
	role := flag.String("role", "", "one of ADMIN, POLICE, FORENSICS, LAWYER, JUDGE (required)")
	flag.Parse()

	if err := run(*email, *password, *first, *last, *role); err != nil {
		fmt.Fprintln(os.Stderr, "devuser: "+err.Error())
		os.Exit(1)
	}
}

func run(email, password, first, last, role string) error {
	email = strings.TrimSpace(email)
	role = strings.ToUpper(strings.TrimSpace(role))

	if email == "" || password == "" || first == "" || last == "" || role == "" {
		return fmt.Errorf("usage: devuser -email=... -password=... -first=... -last=... -role=<ADMIN|POLICE|FORENSICS|LAWYER|JUDGE>")
	}
	if len(password) < 8 {
		return fmt.Errorf("password must be at least 8 characters (matches the login flow's own minimum)")
	}
	if !validRoles[role] {
		return fmt.Errorf("role %q is not one of ADMIN, POLICE, FORENSICS, LAWYER, JUDGE", role)
	}

	cfg, err := config.LoadMigrator()
	if err != nil {
		return fmt.Errorf("load migrator config: %w", err)
	}

	// bcrypt's cost doesn't need to match the running server's configured
	// BCRYPT_COST — auth.VerifyPassword (bcrypt.CompareHashAndPassword)
	// reads the cost back out of the stored hash itself.
	hash, err := auth.HashPassword(password, 10)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, cfg.DSN())
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer pool.Close()

	var userID string
	err = pool.QueryRow(ctx, `
		INSERT INTO users (email, password_hash, first_name, last_name, status)
		VALUES ($1, $2, $3, $4, 'active')
		ON CONFLICT ON CONSTRAINT users_email_unique DO UPDATE
			SET password_hash = EXCLUDED.password_hash,
			    first_name    = EXCLUDED.first_name,
			    last_name     = EXCLUDED.last_name,
			    status        = 'active',
			    updated_at    = now()
		RETURNING id`,
		email, hash, first, last,
	).Scan(&userID)
	if err != nil {
		return fmt.Errorf("upsert user: %w", err)
	}

	tag, err := pool.Exec(ctx, `
		INSERT INTO user_roles (user_id, role_id)
		SELECT $1, id FROM roles WHERE name = $2
		ON CONFLICT ON CONSTRAINT user_roles_user_role_unique DO NOTHING`,
		userID, role,
	)
	if err != nil {
		return fmt.Errorf("assign role: %w", err)
	}
	if tag.RowsAffected() == 0 {
		// Either the role was already assigned (fine) or the role name
		// doesn't exist in the roles table (the seed data wasn't applied)
		// — distinguish the two so a real setup problem isn't swallowed.
		var exists bool
		if err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM roles WHERE name = $1)`, role).Scan(&exists); err != nil {
			return fmt.Errorf("check role catalog: %w", err)
		}
		if !exists {
			return fmt.Errorf("role %q does not exist in the roles table — run scripts/seed_db.sh first", role)
		}
	}

	fmt.Printf("devuser: ready — id=%s email=%s role=%s\n", userID, email, role)
	return nil
}
