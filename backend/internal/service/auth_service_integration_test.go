//go:build integration

// Run with: go test -tags=integration ./internal/service/...
// Requires the docker-compose postgres service up, migrated (go run
// ./cmd/migrate up, including 000002_auth_sessions), with evidentia_app's
// password left at the migration's default ('changeme_example').
//
// Add -p 1 when running alongside other packages' integration tests in one
// invocation (go test -tags=integration ./...) — this package and
// backend/tests both truncate/repopulate the shared users/auth_sessions
// tables in the same live database, and Go runs different packages'
// tests concurrently by default. See backend/tests/helpers_test.go for
// the full explanation.
package service

import (
	"context"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"evidentia/backend/internal/audit"
	"evidentia/backend/internal/auth"
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

func testPool(t *testing.T, dsn string) *pgxpool.Pool {
	t.Helper()
	pool, err := pgxpool.New(context.Background(), dsn)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	require.NoError(t, pool.Ping(context.Background()))
	return pool
}

func migratorPool(t *testing.T) *pgxpool.Pool {
	dsn := "postgres://" + envOr("DATABASE_MIGRATOR_USER", "evidentia") +
		":" + envOr("DATABASE_MIGRATOR_PASSWORD", "changeme_example") +
		"@" + envOr("DATABASE_HOST", "localhost") + ":" + envOr("DATABASE_PORT", "5432") +
		"/" + envOr("DATABASE_NAME", "evidentia") + "?sslmode=disable"
	return testPool(t, dsn)
}

func appPool(t *testing.T) *pgxpool.Pool {
	dsn := "postgres://evidentia_app:" + envOr("DATABASE_APP_PASSWORD", "changeme_example") +
		"@" + envOr("DATABASE_HOST", "localhost") + ":" + envOr("DATABASE_PORT", "5432") +
		"/" + envOr("DATABASE_NAME", "evidentia") + "?sslmode=disable"
	return testPool(t, dsn)
}

func truncateIdentityTables(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	_, err := pool.Exec(context.Background(),
		`TRUNCATE TABLE auth_sessions, user_roles, users RESTART IDENTITY CASCADE`)
	require.NoError(t, err)
}

const testPassword = "correct horse battery staple"

// seedUser inserts a user with a real bcrypt hash of testPassword (cost 4
// — bcrypt's minimum, fast enough for tests; production always uses
// BCRYPT_COST, validated by internal/config to be >= 10) and optionally
// assigns roleName if non-empty (the role must already exist — see
// backend/db/seed/001_reference_data.sql).
func seedUser(t *testing.T, migrator *pgxpool.Pool, email, status, roleName string) uuid.UUID {
	t.Helper()
	ctx := context.Background()

	hash, err := auth.HashPassword(testPassword, 4)
	require.NoError(t, err)

	var userID uuid.UUID
	require.NoError(t, migrator.QueryRow(ctx,
		`INSERT INTO users (email, password_hash, first_name, last_name, status) VALUES ($1, $2, 'Test', 'User', $3) RETURNING id`,
		email, hash, status,
	).Scan(&userID))

	if roleName != "" {
		var roleID uuid.UUID
		err := migrator.QueryRow(ctx, `SELECT id FROM roles WHERE name = $1`, roleName).Scan(&roleID)
		if err != nil {
			// Reference data not seeded in this environment — create the
			// role directly rather than depending on seed order.
			require.NoError(t, migrator.QueryRow(ctx,
				`INSERT INTO roles (name) VALUES ($1) ON CONFLICT ON CONSTRAINT roles_name_unique DO UPDATE SET name = EXCLUDED.name RETURNING id`,
				roleName,
			).Scan(&roleID))
		}
		_, err = migrator.Exec(ctx, `INSERT INTO user_roles (user_id, role_id) VALUES ($1, $2)`, userID, roleID)
		require.NoError(t, err)
	}

	return userID
}

func newTestAuthService(app *pgxpool.Pool) *AuthService {
	jwtManager := auth.NewJWTManager("test-signing-key-at-least-32-characters-long", "evidentia-api", "evidentia-client", 15*time.Minute)
	// Limits high enough that no existing test's handful of Login calls
	// could ever trip the throttle by accident — tests exercising the
	// throttle itself live in auth_service_ratelimit_test.go and build
	// their own AuthService with tight limits.
	limits := config.LoginRateLimitConfig{IPMax: 1000, IPWindow: time.Minute, AccountMax: 1000, AccountWindow: time.Minute}
	return NewAuthService(app, jwtManager, 4, 7*24*time.Hour, audit.NewSlogRecorder(discardLogger()), newFakeLimiter(), limits)
}

func TestAuthService_Login_ValidCredentialsSucceed(t *testing.T) {
	migrator := migratorPool(t)
	truncateIdentityTables(t, migrator)
	app := appPool(t)
	svc := newTestAuthService(app)

	userID := seedUser(t, migrator, "valid@example.com", models.UserStatusActive, models.RoleLawyer)

	result, err := svc.Login(context.Background(), "valid@example.com", testPassword, "127.0.0.1", "test-agent")
	require.NoError(t, err)
	assert.NotEmpty(t, result.AccessToken)
	assert.NotEmpty(t, result.RefreshToken)
	assert.Equal(t, userID, result.User.ID)
	assert.Equal(t, models.RoleLawyer, result.User.Role)
	assert.WithinDuration(t, time.Now().Add(15*time.Minute), result.AccessExpiresAt, 2*time.Second)
}

func TestAuthService_Login_WrongPasswordFails(t *testing.T) {
	migrator := migratorPool(t)
	truncateIdentityTables(t, migrator)
	app := appPool(t)
	svc := newTestAuthService(app)

	seedUser(t, migrator, "wrongpass@example.com", models.UserStatusActive, "")

	_, err := svc.Login(context.Background(), "wrongpass@example.com", "not-the-password", "", "")
	require.Error(t, err)
	assertGenericAuthError(t, err)
}

func TestAuthService_Login_UnknownEmailFailsGenerically(t *testing.T) {
	migrator := migratorPool(t)
	truncateIdentityTables(t, migrator)
	app := appPool(t)
	svc := newTestAuthService(app)

	_, err := svc.Login(context.Background(), "nobody@example.com", testPassword, "", "")
	require.Error(t, err)
	assertGenericAuthError(t, err)
}

func TestAuthService_Login_InactiveUserFails(t *testing.T) {
	migrator := migratorPool(t)
	truncateIdentityTables(t, migrator)
	app := appPool(t)
	svc := newTestAuthService(app)

	seedUser(t, migrator, "inactive@example.com", models.UserStatusInactive, "")

	_, err := svc.Login(context.Background(), "inactive@example.com", testPassword, "", "")
	require.Error(t, err)
	assertGenericAuthError(t, err)
}

func TestAuthService_Login_SuspendedUserFails(t *testing.T) {
	migrator := migratorPool(t)
	truncateIdentityTables(t, migrator)
	app := appPool(t)
	svc := newTestAuthService(app)

	seedUser(t, migrator, "suspended@example.com", models.UserStatusSuspended, "")

	_, err := svc.Login(context.Background(), "suspended@example.com", testPassword, "", "")
	require.Error(t, err)
	assertGenericAuthError(t, err)
}

func TestAuthService_Login_NeverLeaksAccountExistence(t *testing.T) {
	migrator := migratorPool(t)
	truncateIdentityTables(t, migrator)
	app := appPool(t)
	svc := newTestAuthService(app)

	seedUser(t, migrator, "exists@example.com", models.UserStatusActive, "")

	_, err1 := svc.Login(context.Background(), "exists@example.com", "wrong-password", "", "")
	_, err2 := svc.Login(context.Background(), "does-not-exist@example.com", "wrong-password", "", "")

	require.Error(t, err1)
	require.Error(t, err2)
	assert.Equal(t, err1.Error(), err2.Error(), "wrong-password-for-real-user and unknown-email must be indistinguishable to the caller")
}

func TestAuthService_RefreshRotation_OldTokenRejectedAfterRotation(t *testing.T) {
	migrator := migratorPool(t)
	truncateIdentityTables(t, migrator)
	app := appPool(t)
	svc := newTestAuthService(app)

	seedUser(t, migrator, "rotate@example.com", models.UserStatusActive, models.RoleLawyer)
	ctx := context.Background()

	login, err := svc.Login(ctx, "rotate@example.com", testPassword, "", "")
	require.NoError(t, err)

	refreshed, err := svc.Refresh(ctx, login.RefreshToken, "", "")
	require.NoError(t, err, "the freshly issued refresh token must work exactly once")
	assert.NotEqual(t, login.RefreshToken, refreshed.RefreshToken, "rotation must issue a NEW refresh token")
	assert.NotEqual(t, login.AccessToken, refreshed.AccessToken)

	// This is master prompt §61's required replay test: reusing the
	// now-rotated-away original token must be rejected.
	_, err = svc.Refresh(ctx, login.RefreshToken, "", "")
	require.Error(t, err, "the OLD refresh token must no longer work after rotation")
	assertGenericRefreshError(t, err)
}

func TestAuthService_RefreshReuseDetection_RevokesWholeFamily(t *testing.T) {
	migrator := migratorPool(t)
	truncateIdentityTables(t, migrator)
	app := appPool(t)
	svc := newTestAuthService(app)

	seedUser(t, migrator, "reuse@example.com", models.UserStatusActive, models.RoleLawyer)
	ctx := context.Background()

	login, err := svc.Login(ctx, "reuse@example.com", testPassword, "", "")
	require.NoError(t, err)

	rotated, err := svc.Refresh(ctx, login.RefreshToken, "", "")
	require.NoError(t, err)

	// Reuse the original (now-revoked) token — triggers family revocation.
	_, err = svc.Refresh(ctx, login.RefreshToken, "", "")
	require.Error(t, err)

	// The token from the legitimate rotation must ALSO now be rejected,
	// even though it was never itself reused — the whole family was
	// revoked as a compromise response (master prompt §26).
	_, err = svc.Refresh(ctx, rotated.RefreshToken, "", "")
	require.Error(t, err, "the entire token family must be invalidated once reuse is detected")
	assertGenericRefreshError(t, err)
}

func TestAuthService_Refresh_UnknownTokenFails(t *testing.T) {
	app := appPool(t)
	svc := newTestAuthService(app)

	_, err := svc.Refresh(context.Background(), "not-a-real-refresh-token", "", "")
	require.Error(t, err)
	assertGenericRefreshError(t, err)
}

func TestAuthService_Refresh_InactiveUserFails(t *testing.T) {
	migrator := migratorPool(t)
	truncateIdentityTables(t, migrator)
	app := appPool(t)
	svc := newTestAuthService(app)

	seedUser(t, migrator, "deactivate-refresh@example.com", models.UserStatusActive, "")
	ctx := context.Background()

	login, err := svc.Login(ctx, "deactivate-refresh@example.com", testPassword, "", "")
	require.NoError(t, err)

	_, err = migrator.Exec(ctx, `UPDATE users SET status = $1 WHERE email = $2`, models.UserStatusInactive, "deactivate-refresh@example.com")
	require.NoError(t, err)

	_, err = svc.Refresh(ctx, login.RefreshToken, "", "")
	require.Error(t, err, "refresh must fail once the account has been deactivated, even with a valid unexpired session")
	assertGenericRefreshError(t, err)
}

// TestAuthService_ResolveIdentity_RejectsDeactivatedUser is master prompt
// §62's core test: a previously active user, resolved successfully once,
// must be rejected after deactivation — the exact check
// internal/middleware/auth_middleware.go relies on for every request.
func TestAuthService_ResolveIdentity_RejectsDeactivatedUser(t *testing.T) {
	migrator := migratorPool(t)
	truncateIdentityTables(t, migrator)
	app := appPool(t)
	svc := newTestAuthService(app)
	ctx := context.Background()

	userID := seedUser(t, migrator, "deactivate-resolve@example.com", models.UserStatusActive, models.RoleLawyer)

	identity, err := svc.ResolveIdentity(ctx, userID)
	require.NoError(t, err, "an active user must resolve successfully")
	assert.Equal(t, userID, identity.ID)
	assert.Equal(t, []string{models.RoleLawyer}, identity.Roles)

	_, err = migrator.Exec(ctx, `UPDATE users SET status = $1 WHERE id = $2`, models.UserStatusInactive, userID)
	require.NoError(t, err)

	_, err = svc.ResolveIdentity(ctx, userID)
	require.Error(t, err, "the SAME user ID must now be rejected — a previously issued access token must stop authenticating")
}

func TestAuthService_Logout_RevokesSession(t *testing.T) {
	migrator := migratorPool(t)
	truncateIdentityTables(t, migrator)
	app := appPool(t)
	svc := newTestAuthService(app)
	ctx := context.Background()

	userID := seedUser(t, migrator, "logout@example.com", models.UserStatusActive, "")
	login, err := svc.Login(ctx, "logout@example.com", testPassword, "", "")
	require.NoError(t, err)

	require.NoError(t, svc.Logout(ctx, login.RefreshToken, userID))

	// The session is revoked, not deleted (soft-lifecycle, per the
	// migration) — but it must no longer work for refresh.
	_, err = svc.Refresh(ctx, login.RefreshToken, "", "")
	require.Error(t, err, "a logged-out refresh token must no longer authenticate")
	assertGenericRefreshError(t, err)
}

func TestAuthService_Logout_IsIdempotentWithNoToken(t *testing.T) {
	app := appPool(t)
	svc := newTestAuthService(app)

	err := svc.Logout(context.Background(), "", uuid.New())
	assert.NoError(t, err, "logging out with no refresh token is a no-op, not an error")
}

func TestAuthService_Logout_CannotRevokeAnotherUsersSession(t *testing.T) {
	migrator := migratorPool(t)
	truncateIdentityTables(t, migrator)
	app := appPool(t)
	svc := newTestAuthService(app)
	ctx := context.Background()

	seedUser(t, migrator, "victim@example.com", models.UserStatusActive, "")
	attackerID := seedUser(t, migrator, "attacker@example.com", models.UserStatusActive, "")

	victimLogin, err := svc.Login(ctx, "victim@example.com", testPassword, "", "")
	require.NoError(t, err)

	// The attacker calls logout with the VICTIM's refresh token but their
	// OWN authenticated identity (currentUserID) — Logout must not revoke
	// a session belonging to someone else.
	require.NoError(t, svc.Logout(ctx, victimLogin.RefreshToken, attackerID))

	_, err = svc.Refresh(ctx, victimLogin.RefreshToken, "", "")
	assert.NoError(t, err, "the victim's session must remain valid — logout must not have revoked it")
}

func assertGenericAuthError(t *testing.T, err error) {
	t.Helper()
	assert.Contains(t, err.Error(), genericAuthError)
}

func assertGenericRefreshError(t *testing.T, err error) {
	t.Helper()
	assert.Contains(t, err.Error(), genericRefreshError)
}
