//go:build integration

// Run with: go test -tags=integration ./internal/storage/...
// Requires the docker-compose minio service to be up with credentials
// matching the defaults below, or the MINIO_* environment variables set to
// point at a real instance.
package storage

import (
	"bytes"
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"evidentia/backend/internal/config"
)

func integrationMinIOConfig() config.MinIOConfig {
	return config.MinIOConfig{
		Endpoint:  envOr("MINIO_ENDPOINT", "localhost:9000"),
		AccessKey: envOr("MINIO_ACCESS_KEY", "evidentia_minio"),
		SecretKey: envOr("MINIO_SECRET_KEY", "changeme_example"),
		UseSSL:    false,
		Bucket:    envOr("MINIO_BUCKET", "evidentia-documents-test"),
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func TestIntegration_MinIOPutGetDelete(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	s, err := NewMinIO(ctx, integrationMinIOConfig())
	require.NoError(t, err)
	require.NoError(t, s.HealthCheck(ctx))

	content := []byte("integration test object")
	require.NoError(t, s.Put(ctx, "integration/object.bin", bytes.NewReader(content), int64(len(content)), "application/octet-stream"))

	r, err := s.Get(ctx, "integration/object.bin")
	require.NoError(t, err)
	_ = r.Close()

	require.NoError(t, s.Delete(ctx, "integration/object.bin"))
}
