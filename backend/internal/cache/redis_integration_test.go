//go:build integration

// Run with: go test -tags=integration ./internal/cache/...
// Requires the docker-compose redis service to be up.
package cache

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"evidentia/backend/internal/config"
)

func TestIntegration_ConnectAndPing(t *testing.T) {
	addr := os.Getenv("REDIS_ADDR")
	if addr == "" {
		addr = "localhost:6379"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	c, err := New(ctx, config.RedisConfig{Addr: addr})
	require.NoError(t, err)
	defer c.Close()

	require.NoError(t, c.Ping(ctx))
}
