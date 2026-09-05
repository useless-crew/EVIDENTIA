package service

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"evidentia/backend/internal/audit"
	"evidentia/backend/internal/auth"
	"evidentia/backend/internal/config"
	"evidentia/backend/internal/ratelimit"
	"evidentia/backend/internal/utils"
)

// These tests exercise AuthService.checkLoginRateLimit in isolation — see
// that method's doc comment for why it never touches the database,
// letting these run without a real Postgres/Redis connection.

func rateLimitTestService(limits config.LoginRateLimitConfig, limiter ratelimit.Limiter) *AuthService {
	jwtManager := auth.NewJWTManager("test-signing-key-at-least-32-characters-long", "evidentia-api", "evidentia-client", 15*time.Minute)
	recorder := audit.NewSlogRecorder(slog.New(slog.NewTextHandler(io.Discard, nil)))
	return NewAuthService(nil, jwtManager, 4, 7*24*time.Hour, recorder, limiter, limits)
}

func assertNotRateLimited(t *testing.T, err *utils.AppError) {
	t.Helper()
	if err != nil {
		assert.NotEqual(t, http.StatusTooManyRequests, err.Status)
	}
}

func TestAuthService_CheckLoginRateLimit_IPBlocksAfterThreshold(t *testing.T) {
	limits := config.LoginRateLimitConfig{IPMax: 2, IPWindow: time.Minute, AccountMax: 1000, AccountWindow: time.Minute}
	svc := rateLimitTestService(limits, newFakeLimiter())
	ctx := context.Background()

	for i := 0; i < 2; i++ {
		err := svc.checkLoginRateLimit(ctx, "203.0.113.5", "login:acct:victim1")
		assertNotRateLimited(t, err)
	}

	err := svc.checkLoginRateLimit(ctx, "203.0.113.5", "login:acct:victim1")
	require.NotNil(t, err)
	assert.Equal(t, http.StatusTooManyRequests, err.Status)
	assert.Equal(t, utils.CodeTooManyRequests, err.Code)
	assert.Greater(t, err.RetryAfter, time.Duration(0))
}

func TestAuthService_CheckLoginRateLimit_IPIsPerIPNotGlobal(t *testing.T) {
	limits := config.LoginRateLimitConfig{IPMax: 1, IPWindow: time.Minute, AccountMax: 1000, AccountWindow: time.Minute}
	svc := rateLimitTestService(limits, newFakeLimiter())
	ctx := context.Background()

	assertNotRateLimited(t, svc.checkLoginRateLimit(ctx, "198.51.100.1", "login:acct:a"))
	// A different source IP has its own, untouched budget even against
	// the same account key.
	assertNotRateLimited(t, svc.checkLoginRateLimit(ctx, "198.51.100.2", "login:acct:a"))
}

func TestAuthService_CheckLoginRateLimit_AccountBlocksAfterThreshold(t *testing.T) {
	limits := config.LoginRateLimitConfig{IPMax: 1000, IPWindow: time.Minute, AccountMax: 2, AccountWindow: time.Minute}
	svc := rateLimitTestService(limits, newFakeLimiter())
	ctx := context.Background()

	// Distinct source IPs, SAME target account key — proves the account
	// throttle is independent of source IP (a distributed brute-force
	// against one victim cannot dodge it by rotating IPs).
	ips := []string{"203.0.113.10", "203.0.113.11"}
	for _, ip := range ips {
		assertNotRateLimited(t, svc.checkLoginRateLimit(ctx, ip, "login:acct:victim2"))
	}

	err := svc.checkLoginRateLimit(ctx, "203.0.113.12", "login:acct:victim2")
	require.NotNil(t, err)
	assert.Equal(t, http.StatusTooManyRequests, err.Status)
}

func TestAuthService_CheckLoginRateLimit_AccountResetAfterSuccess(t *testing.T) {
	limits := config.LoginRateLimitConfig{IPMax: 1000, IPWindow: time.Minute, AccountMax: 1, AccountWindow: time.Minute}
	limiter := newFakeLimiter()
	svc := rateLimitTestService(limits, limiter)
	ctx := context.Background()

	assertNotRateLimited(t, svc.checkLoginRateLimit(ctx, "203.0.113.20", "login:acct:victim3"))

	err := svc.checkLoginRateLimit(ctx, "203.0.113.21", "login:acct:victim3")
	require.NotNil(t, err)
	assert.Equal(t, http.StatusTooManyRequests, err.Status, "budget should be exhausted before reset")

	require.NoError(t, svc.loginLimit.Reset(ctx, "login:acct:victim3"))

	assertNotRateLimited(t, svc.checkLoginRateLimit(ctx, "203.0.113.22", "login:acct:victim3"))
}

func TestAuthService_CheckLoginRateLimit_FailsOpenOnBackendError(t *testing.T) {
	limits := config.LoginRateLimitConfig{IPMax: 0, IPWindow: time.Minute, AccountMax: 0, AccountWindow: time.Minute}
	svc := rateLimitTestService(limits, erroringLimiter{})

	// IPMax/AccountMax of 0 would reject on the very first attempt if the
	// limiter reported a real (allowed=false, err=nil) result — proving
	// this test only passes because the error path fails OPEN.
	err := svc.checkLoginRateLimit(context.Background(), "203.0.113.30", "login:acct:whoever")
	assert.Nil(t, err, "a throttle backend error must fail OPEN, never block login outright")
}

// erroringLimiter always reports an error, simulating a Redis outage.
type erroringLimiter struct{}

func (erroringLimiter) Allow(context.Context, string, int, time.Duration) (bool, time.Duration, error) {
	return true, 0, assert.AnError
}

func (erroringLimiter) Reset(context.Context, string) error { return nil }
