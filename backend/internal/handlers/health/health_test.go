package health

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakePinger and fakeHealthChecker let tests drive readiness dependency
// behavior directly, without a real database, Redis, or MinIO — see master
// prompt §31 ("Test dependency health behavior using mocks/fakes").
type fakePinger struct{ err error }

func (f fakePinger) Ping(context.Context) error { return f.err }

type fakeHealthChecker struct{ err error }

func (f fakeHealthChecker) HealthCheck(context.Context) error { return f.err }

func init() {
	gin.SetMode(gin.TestMode)
}

func TestLiveness_ReturnsOK(t *testing.T) {
	r := gin.New()
	r.GET("/health", Liveness("evidentia-backend", "1.2.3"))

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var body livenessResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "ok", body.Status)
	assert.Equal(t, "evidentia-backend", body.Service)
	assert.Equal(t, "1.2.3", body.Version)
}

func TestReadiness_AllHealthy(t *testing.T) {
	r := gin.New()
	r.GET("/ready", Readiness(fakePinger{}, fakePinger{}, fakeHealthChecker{}))

	req := httptest.NewRequest(http.MethodGet, "/ready", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var body readinessResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "ready", body.Status)
	assert.Equal(t, "ok", body.Dependencies[depPostgres])
	assert.Equal(t, "ok", body.Dependencies[depRedis])
	assert.Equal(t, "ok", body.Dependencies[depMinio])
}

func TestReadiness_ReportsFailingDependency(t *testing.T) {
	r := gin.New()
	r.GET("/ready", Readiness(
		fakePinger{err: errors.New("connection refused")},
		fakePinger{},
		fakeHealthChecker{},
	))

	req := httptest.NewRequest(http.MethodGet, "/ready", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	require.Equal(t, http.StatusServiceUnavailable, rec.Code)

	var body readinessResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "not_ready", body.Status)
	assert.Equal(t, "error", body.Dependencies[depPostgres])
	assert.Equal(t, "ok", body.Dependencies[depRedis])

	// The failing driver error must never reach the client.
	assert.NotContains(t, rec.Body.String(), "connection refused")
}

func TestReadiness_ReportsAllFailingDependencies(t *testing.T) {
	r := gin.New()
	failErr := errors.New("unreachable")
	r.GET("/ready", Readiness(
		fakePinger{err: failErr},
		fakePinger{err: failErr},
		fakeHealthChecker{err: failErr},
	))

	req := httptest.NewRequest(http.MethodGet, "/ready", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	require.Equal(t, http.StatusServiceUnavailable, rec.Code)

	var body readinessResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "error", body.Dependencies[depPostgres])
	assert.Equal(t, "error", body.Dependencies[depRedis])
	assert.Equal(t, "error", body.Dependencies[depMinio])
}
