package cache

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"evidentia/backend/internal/config"
)

// unusedAddr returns a loopback host:port with no listener, so connection
// attempts fail fast and deterministically without requiring Docker.
func unusedAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := l.Addr().String()
	require.NoError(t, l.Close())
	return addr
}

func TestNew_FailsFastWhenUnreachable(t *testing.T) {
	cfg := config.RedisConfig{Addr: unusedAddr(t), DB: 0}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	c, err := New(ctx, cfg)
	require.Error(t, err)
	assert.Nil(t, c)
}
