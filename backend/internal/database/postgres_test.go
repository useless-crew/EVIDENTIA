package database

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"evidentia/backend/internal/config"
)

// unusedAddr returns a loopback host:port that is guaranteed to have no
// listener, so connection attempts fail fast and deterministically without
// requiring Docker or a real PostgreSQL instance.
func unusedAddr(t *testing.T) (string, int) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := l.Addr().(*net.TCPAddr)
	require.NoError(t, l.Close())
	return addr.IP.String(), addr.Port
}

func TestNew_FailsFastWhenUnreachable(t *testing.T) {
	host, port := unusedAddr(t)
	cfg := config.DatabaseConfig{
		Host:            host,
		Port:            port,
		User:            "evidentia",
		Password:        "s3cret",
		Name:            "evidentia",
		SSLMode:         "disable",
		MaxOpenConns:    5,
		MaxIdleConns:    1,
		ConnMaxLifetime: time.Minute,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	db, err := New(ctx, cfg)
	require.Error(t, err)
	assert.Nil(t, db)
}

func TestNew_RejectsInvalidSSLMode(t *testing.T) {
	host, port := unusedAddr(t)
	cfg := config.DatabaseConfig{
		Host:     host,
		Port:     port,
		User:     "evidentia",
		Password: "s3cret",
		Name:     "evidentia",
		SSLMode:  "not-a-real-mode",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	_, err := New(ctx, cfg)
	require.Error(t, err)
}
