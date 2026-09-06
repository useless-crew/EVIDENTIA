//go:build integration

// Run with: go test -tags=integration ./internal/httpserver/...
// Requires the full docker-compose infrastructure (postgres, redis, minio)
// to be up with credentials matching docker-compose.yml's defaults, or the
// corresponding environment variables set to point at real instances.
//
// This exercises the fully wired application (config -> app container ->
// router) end to end, rather than a single package in isolation — see
// also auth_flow_integration_test.go.
package httpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"evidentia/backend/internal/app"
)

func setenvIfUnset(t *testing.T, key, val string) {
	t.Helper()
	if _, ok := os.LookupEnv(key); !ok {
		t.Setenv(key, val)
	}
}

func TestIntegration_HealthAndReadyEndToEnd(t *testing.T) {
	setenvIfUnset(t, "DATABASE_USER", "evidentia")
	setenvIfUnset(t, "DATABASE_PASSWORD", "changeme_example")
	setenvIfUnset(t, "DATABASE_NAME", "evidentia")
	setenvIfUnset(t, "MINIO_ACCESS_KEY", "evidentia_minio")
	setenvIfUnset(t, "MINIO_SECRET_KEY", "changeme_example")
	setenvIfUnset(t, "MINIO_BUCKET", "evidentia-documents")
	setenvIfUnset(t, "REDIS_PASSWORD", "changeme_example")
	setenvIfUnset(t, "LOGIN_RATE_LIMIT_IP_MAX", "1000000")
	setenvIfUnset(t, "LOGIN_RATE_LIMIT_ACCOUNT_MAX", "1000000")
	setenvIfUnset(t, "JWT_SIGNING_KEY", "test_signing_key_padded_to_32_chars_min")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	application, err := app.New(ctx)
	require.NoError(t, err)
	defer application.Close()

	router := NewRouter(application)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	req = httptest.NewRequest(http.MethodGet, "/ready", nil)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Equal(t, "ready", body["status"])
}
