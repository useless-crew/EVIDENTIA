// Package bootstrap implements Evidentia's one-time initial-administrator
// provisioning — the ONLY account this project ever creates outside normal
// admin-driven user management (see internal/service.UserService). Every
// other user, including every other ADMIN, is created by an existing
// ADMIN through POST /admin/users.
package bootstrap

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"evidentia/backend/db/generated"
	"evidentia/backend/internal/audit"
	"evidentia/backend/internal/auth"
	"evidentia/backend/internal/config"
	"evidentia/backend/internal/models"
	"evidentia/backend/internal/repository"
)

// EnsureBootstrapAdmin creates the initial ADMIN account if, and only if,
// no user holding the ADMIN role exists yet — checked and created inside
// one transaction, so a concurrent call (e.g. two replicas starting at
// once) can never create two bootstrap admins. It is safe to call on every
// process startup:
//
//   - If an ADMIN already exists, this is a no-op — it never resets a
//     password, renames anyone, or otherwise touches an existing account
//     (master prompt §29: "do not overwrite existing Admin passwords").
//   - If no ADMIN exists and cfg is fully unset, this is a no-op — the
//     deployment is expected to provision one another way (e.g.
//     backend/cmd/devuser for local dev).
//   - If no ADMIN exists and cfg is fully set (config.validate already
//     rejects a partial group before this ever runs), it creates the user,
//     bcrypt-hashes the password at bcryptCost, assigns ADMIN, and records
//     an ADMIN_BOOTSTRAPPED audit event — never logging the password.
func EnsureBootstrapAdmin(ctx context.Context, pool *pgxpool.Pool, cfg config.BootstrapAdminConfig, bcryptCost int, recorder audit.Recorder, logger *slog.Logger) error {
	if cfg.Email == "" && cfg.Password == "" && cfg.Name == "" {
		logger.Info("bootstrap: no EVIDENTIA_BOOTSTRAP_ADMIN_* configured, skipping")
		return nil
	}

	firstName, lastName := splitName(cfg.Name)
	hash, err := auth.HashPassword(cfg.Password, bcryptCost)
	if err != nil {
		return fmt.Errorf("bootstrap: hash password: %w", err)
	}

	var created bool
	err = repository.WithTx(ctx, pool, repository.AppIdentity{}, func(ctx context.Context, q *generated.Queries) error {
		exists, err := q.AdminUserExists(ctx)
		if err != nil {
			return fmt.Errorf("check existing admin: %w", err)
		}
		if exists {
			return nil
		}

		user, err := q.CreateUser(ctx, generated.CreateUserParams{
			Email:        cfg.Email,
			PasswordHash: hash,
			FirstName:    firstName,
			LastName:     lastName,
		})
		if err != nil {
			return fmt.Errorf("create bootstrap admin: %w", err)
		}

		role, err := q.GetRoleByName(ctx, models.RoleAdmin)
		if err != nil {
			return fmt.Errorf("load ADMIN role (has the reference-data seed been applied?): %w", err)
		}
		if err := q.AssignRoleToUser(ctx, generated.AssignRoleToUserParams{UserID: user.ID, RoleID: role.ID}); err != nil {
			return fmt.Errorf("assign ADMIN role: %w", err)
		}

		created = true
		recorder.Record(ctx, audit.Event{
			Action: "ADMIN_BOOTSTRAPPED", ResourceType: "user", ResourceID: &user.ID,
			Role:     models.RoleAdmin,
			Metadata: map[string]any{"email": user.Email},
		})
		return nil
	})
	if err != nil {
		return fmt.Errorf("bootstrap: %w", err)
	}

	if created {
		logger.Info("bootstrap: created initial ADMIN account", slog.String("email", cfg.Email))
	} else {
		logger.Info("bootstrap: an ADMIN account already exists, skipping")
	}
	return nil
}

// splitName splits a display name into (first, last) on the first space.
// A single-word name (no space) becomes first_name only, with last_name
// falling back to the same word — users.last_name is NOT NULL and this
// avoids rejecting an otherwise-valid EVIDENTIA_BOOTSTRAP_ADMIN_NAME value
// outright; the operator can always give the account a fuller name later
// via PUT /admin/users/:id like any other user's profile.
func splitName(name string) (first, last string) {
	name = strings.TrimSpace(name)
	parts := strings.SplitN(name, " ", 2)
	if len(parts) == 1 {
		return parts[0], parts[0]
	}
	return parts[0], strings.TrimSpace(parts[1])
}
