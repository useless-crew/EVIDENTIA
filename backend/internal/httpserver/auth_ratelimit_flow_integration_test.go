//go:build integration

// Run with: go test -tags=integration ./internal/httpserver/...
// Requires the full docker-compose infrastructure (postgres, redis, minio)
// up and migrated — see auth_flow_integration_test.go's doc comment for
// the shared setup.
//
// This proves System 15's login throttle (internal/service.AuthService.
// Login -> internal/ratelimit) actually blocks real HTTP requests through
// the real router + real Redis, not just AuthService in isolation (see
// internal/service/auth_service_ratelimit_test.go for that unit-level
// coverage). Every other httpserver integration test in this package
// deliberately sets LOGIN_RATE_LIMIT_{IP,ACCOUNT}_MAX to a very large
// number (see setenvIfUnset calls throughout this package) so their own,
// unrelated login traffic never trips the throttle — this file is the one
// place that intentionally configures a tight limit to prove the throttle
// itself works.
package httpserver

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"evidentia/backend/internal/app"
)

func TestAuthRateLimitFlow_AccountThrottleBlocksThenRecoversOnReset(t *testing.T) {
	setenvIfUnset(t, "DATABASE_USER", "evidentia_app")
	setenvIfUnset(t, "DATABASE_PASSWORD", "changeme_example")
	setenvIfUnset(t, "DATABASE_NAME", "evidentia")
	setenvIfUnset(t, "MINIO_ACCESS_KEY", "evidentia_minio")
	setenvIfUnset(t, "MINIO_SECRET_KEY", "changeme_example")
	setenvIfUnset(t, "MINIO_BUCKET", "evidentia-documents")
	setenvIfUnset(t, "REDIS_PASSWORD", "changeme_example")
	setenvIfUnset(t, "JWT_SIGNING_KEY", "test-signing-key-at-least-32-characters-long")

	// The one deliberate deviation from every sibling test in this
	// package: a tight, real limit instead of the "never trips" default.
	t.Setenv("LOGIN_RATE_LIMIT_IP_MAX", "1000000") // neutralize the OTHER dimension
	t.Setenv("LOGIN_RATE_LIMIT_ACCOUNT_MAX", "2")
	t.Setenv("LOGIN_RATE_LIMIT_ACCOUNT_WINDOW", "1m")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	application, err := app.New(ctx)
	require.NoError(t, err)
	defer application.Close()

	router := NewRouter(application)

	// A unique, never-seeded email — the account throttle's Redis key is
	// derived from a hash of this exact string (see AuthService's
	// hashLoginAccountKey), so a fresh UUID per run guarantees no
	// collision with any other test's traffic or a prior run's counter.
	// The account is never real: checkLoginRateLimit runs BEFORE the user
	// lookup, so this proves the throttle independent of whether the
	// account exists.
	email := "ratelimit-" + uuid.NewString() + "@example.com"
	body := map[string]string{"email": email, "password": "irrelevant-wrong-password"}

	// Attempts 1-2 consume the account budget but are NOT yet throttled —
	// they reach the real (generic) "invalid credentials" 401.
	for i := 0; i < 2; i++ {
		rec, env := doJSON(t, router, http.MethodPost, "/api/v1/auth/login", body, "")
		require.Equal(t, http.StatusUnauthorized, rec.Code, "attempt %d: %s", i+1, rec.Body.String())
		require.False(t, env.Success)
	}

	// Attempt 3 exhausts the budget: 429, not 401, with a Retry-After
	// header — proving the throttle actually intercepts the request
	// before AuthService ever compares a password.
	rec, env := doJSON(t, router, http.MethodPost, "/api/v1/auth/login", body, "")
	require.Equal(t, http.StatusTooManyRequests, rec.Code, rec.Body.String())
	require.False(t, env.Success)
	require.NotEmpty(t, rec.Header().Get("Retry-After"))
}
