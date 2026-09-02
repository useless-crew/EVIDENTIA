//go:build integration

// Run with: go test -tags=integration ./internal/database/...
// Requires the docker-compose postgres service to be up (see
// docker-compose.yml) with credentials matching the defaults below, or the
// DATABASE_* environment variables set to point at a real instance.
package database

import (
	"context"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"evidentia/backend/internal/config"
)

func integrationDatabaseConfig() config.DatabaseConfig {
	port, _ := strconv.Atoi(envOr("DATABASE_PORT", "5432"))
	return config.DatabaseConfig{
		Host:            envOr("DATABASE_HOST", "localhost"),
		Port:            port,
		User:            envOr("DATABASE_USER", "evidentia"),
		Password:        envOr("DATABASE_PASSWORD", "changeme_example"),
		Name:            envOr("DATABASE_NAME", "evidentia"),
		SSLMode:         "disable",
		MaxOpenConns:    5,
		MaxIdleConns:    1,
		ConnMaxLifetime: time.Minute,
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func TestIntegration_ConnectAndPing(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	db, err := New(ctx, integrationDatabaseConfig())
	require.NoError(t, err)
	defer db.Close()

	require.NoError(t, db.Ping(ctx))
}
