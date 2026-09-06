//go:build integration

// Run with: go test -tags=integration ./internal/ratelimit/...
// Requires the docker-compose redis service to be up.
package ratelimit

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testRedisClient(t *testing.T) *redis.Client {
	t.Helper()
	addr := os.Getenv("REDIS_ADDR")
	if addr == "" {
		addr = "localhost:6379"
	}
	client := redis.NewClient(&redis.Options{Addr: addr, Password: os.Getenv("REDIS_PASSWORD")})
	t.Cleanup(func() { _ = client.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	require.NoError(t, client.Ping(ctx).Err())
	return client
}

func TestIntegration_RedisLimiter_AllowsUpToLimitThenBlocks(t *testing.T) {
	client := testRedisClient(t)
	limiter := NewRedisLimiter(client)
	ctx := context.Background()
	key := "test:" + uuid.NewString()
	t.Cleanup(func() { _ = limiter.Reset(context.Background(), key) })

	for i := 0; i < 3; i++ {
		allowed, _, err := limiter.Allow(ctx, key, 3, time.Minute)
		require.NoError(t, err)
		assert.True(t, allowed, "attempt %d should be allowed", i+1)
	}

	allowed, retryAfter, err := limiter.Allow(ctx, key, 3, time.Minute)
	require.NoError(t, err)
	assert.False(t, allowed)
	assert.Greater(t, retryAfter, time.Duration(0))
}

func TestIntegration_RedisLimiter_ResetRestoresFullBudget(t *testing.T) {
	client := testRedisClient(t)
	limiter := NewRedisLimiter(client)
	ctx := context.Background()
	key := "test:" + uuid.NewString()
	t.Cleanup(func() { _ = limiter.Reset(context.Background(), key) })

	allowed, _, err := limiter.Allow(ctx, key, 1, time.Minute)
	require.NoError(t, err)
	require.True(t, allowed)

	allowed, _, err = limiter.Allow(ctx, key, 1, time.Minute)
	require.NoError(t, err)
	require.False(t, allowed, "budget of 1 should already be exhausted")

	require.NoError(t, limiter.Reset(ctx, key))

	allowed, _, err = limiter.Allow(ctx, key, 1, time.Minute)
	require.NoError(t, err)
	assert.True(t, allowed, "a fresh budget should be available immediately after Reset")
}

func TestIntegration_RedisLimiter_KeysAreIndependent(t *testing.T) {
	client := testRedisClient(t)
	limiter := NewRedisLimiter(client)
	ctx := context.Background()
	keyA := "test:" + uuid.NewString()
	keyB := "test:" + uuid.NewString()
	t.Cleanup(func() {
		_ = limiter.Reset(context.Background(), keyA)
		_ = limiter.Reset(context.Background(), keyB)
	})

	allowed, _, err := limiter.Allow(ctx, keyA, 1, time.Minute)
	require.NoError(t, err)
	require.True(t, allowed)
	allowed, _, err = limiter.Allow(ctx, keyA, 1, time.Minute)
	require.NoError(t, err)
	require.False(t, allowed)

	// keyB has its own, untouched budget.
	allowed, _, err = limiter.Allow(ctx, keyB, 1, time.Minute)
	require.NoError(t, err)
	assert.True(t, allowed)
}
