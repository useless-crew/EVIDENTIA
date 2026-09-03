//go:build integration

// Run with: go test -tags=integration ./internal/bootstrap/...
// Requires the docker-compose postgres service up, migrated, seeded. Add
// -p 1 when running alongside other packages' integration tests — this
// file truncates/repopulates the shared users/roles tables.
package bootstrap

import (
	"context"
	"io"
	"log/slog"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"evidentia/backend/internal/audit"
	"evidentia/backend/internal/config"
	"evidentia/backend/internal/models"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func migratorPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := "postgres://" + envOr("DATABASE_MIGRATOR_USER", "evidentia") +
		":" + envOr("DATABASE_MIGRATOR_PASSWORD", "changeme_example") +
		"@" + envOr("DATABASE_HOST", "localhost") + ":" + envOr("DATABASE_PORT", "5432") +
		"/" + envOr("DATABASE_NAME", "evidentia") + "?sslmode=disable"
	pool, err := pgxpool.New(context.Background(), dsn)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	require.NoError(t, pool.Ping(context.Background()))
	return pool
}

func truncateIdentityTables(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	_, err := pool.Exec(context.Background(),
		`TRUNCATE TABLE auth_sessions, user_roles, users RESTART IDENTITY CASCADE`)
	require.NoError(t, err)
}

func adminUserExists(t *testing.T, pool *pgxpool.Pool) bool {
	t.Helper()
	var exists bool
	require.NoError(t, pool.QueryRow(context.Background(), `
		SELECT EXISTS (
			SELECT 1 FROM user_roles ur JOIN roles r ON r.id = ur.role_id WHERE r.name = 'ADMIN'
		)`).Scan(&exists))
	return exists
}

func TestEnsureBootstrapAdmin_UnsetConfigIsNoOp(t *testing.T) {
	pool := migratorPool(t)
	truncateIdentityTables(t, pool)

	err := EnsureBootstrapAdmin(context.Background(), pool, config.BootstrapAdminConfig{}, 4, audit.NewSlogRecorder(discardLogger()), discardLogger())
	require.NoError(t, err)
	assert.False(t, adminUserExists(t, pool), "no admin must be created when EVIDENTIA_BOOTSTRAP_ADMIN_* is entirely unset")
}

func TestEnsureBootstrapAdmin_CreatesInitialAdmin(t *testing.T) {
	pool := migratorPool(t)
	truncateIdentityTables(t, pool)

	recorder := &spyRecorder{}
	cfg := config.BootstrapAdminConfig{Email: "bootstrap-admin@example.com", Password: "a-strong-bootstrap-password-1", Name: "System Administrator"}

	err := EnsureBootstrapAdmin(context.Background(), pool, cfg, 4, recorder, discardLogger())
	require.NoError(t, err)
	assert.True(t, adminUserExists(t, pool))

	var email, firstName, lastName, passwordHash string
	require.NoError(t, pool.QueryRow(context.Background(),
		`SELECT email, first_name, last_name, password_hash FROM users WHERE email = $1`, cfg.Email,
	).Scan(&email, &firstName, &lastName, &passwordHash))
	assert.Equal(t, "System", firstName)
	assert.Equal(t, "Administrator", lastName)
	assert.NotEqual(t, cfg.Password, passwordHash, "the password must be bcrypt-hashed, never stored in plaintext")

	assert.Contains(t, recorder.actions(), "ADMIN_BOOTSTRAPPED")
	for _, e := range recorder.events {
		for _, v := range e.Metadata {
			if s, ok := v.(string); ok {
				assert.NotContains(t, s, cfg.Password, "the bootstrap password must never reach the audit trail")
			}
		}
	}
}

// TestEnsureBootstrapAdmin_IsIdempotent is the core safety guarantee
// (master prompt §29): calling this on every process startup must never
// create a second admin or touch the first one's password once it exists.
func TestEnsureBootstrapAdmin_IsIdempotent(t *testing.T) {
	pool := migratorPool(t)
	truncateIdentityTables(t, pool)
	recorder := &spyRecorder{}
	cfg := config.BootstrapAdminConfig{Email: "idempotent-admin@example.com", Password: "first-password-here-1", Name: "First Admin"}

	require.NoError(t, EnsureBootstrapAdmin(context.Background(), pool, cfg, 4, recorder, discardLogger()))

	var firstHash string
	require.NoError(t, pool.QueryRow(context.Background(), `SELECT password_hash FROM users WHERE email = $1`, cfg.Email).Scan(&firstHash))

	// A second call, even with DIFFERENT credentials, must be a no-op: an
	// ADMIN already exists (from ANY source, not just this function).
	secondCfg := config.BootstrapAdminConfig{Email: "different-admin@example.com", Password: "second-password-here-1", Name: "Second Admin"}
	require.NoError(t, EnsureBootstrapAdmin(context.Background(), pool, secondCfg, 4, recorder, discardLogger()))

	var count int
	require.NoError(t, pool.QueryRow(context.Background(), `SELECT count(*) FROM users`).Scan(&count))
	assert.Equal(t, 1, count, "a second bootstrap attempt must not create a second user")

	var hashAfter string
	require.NoError(t, pool.QueryRow(context.Background(), `SELECT password_hash FROM users WHERE email = $1`, cfg.Email).Scan(&hashAfter))
	assert.Equal(t, firstHash, hashAfter, "the existing admin's password must never be overwritten by a later bootstrap attempt")

	var secondExists bool
	require.NoError(t, pool.QueryRow(context.Background(), `SELECT EXISTS (SELECT 1 FROM users WHERE email = $1)`, secondCfg.Email).Scan(&secondExists))
	assert.False(t, secondExists, "the second config's account must never be created once an admin already exists")
}

// TestEnsureBootstrapAdmin_SkipsWhenNonBootstrapAdminExists confirms the
// no-op check is "any ADMIN", not "an admin this function itself created"
// — e.g. one created via backend/cmd/devuser or POST /admin/users.
func TestEnsureBootstrapAdmin_SkipsWhenNonBootstrapAdminExists(t *testing.T) {
	pool := migratorPool(t)
	truncateIdentityTables(t, pool)
	ctx := context.Background()

	var userID string
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO users (email, password_hash, first_name, last_name, status) VALUES ('manual-admin@example.com', 'x', 'Manual', 'Admin', 'active') RETURNING id`,
	).Scan(&userID))
	_, err := pool.Exec(ctx, `INSERT INTO user_roles (user_id, role_id) SELECT $1, id FROM roles WHERE name = $2`, userID, models.RoleAdmin)
	require.NoError(t, err)

	cfg := config.BootstrapAdminConfig{Email: "should-not-be-created@example.com", Password: "irrelevant-password-1", Name: "Should Not Exist"}
	require.NoError(t, EnsureBootstrapAdmin(ctx, pool, cfg, 4, audit.NewSlogRecorder(discardLogger()), discardLogger()))

	var exists bool
	require.NoError(t, pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM users WHERE email = $1)`, cfg.Email).Scan(&exists))
	assert.False(t, exists)
}

type spyRecorder struct {
	events []audit.Event
}

func (s *spyRecorder) Record(_ context.Context, event audit.Event) {
	s.events = append(s.events, event)
}

func (s *spyRecorder) actions() []string {
	actions := make([]string, len(s.events))
	for i, e := range s.events {
		actions[i] = e.Action
	}
	return actions
}
