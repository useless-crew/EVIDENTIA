package health

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// readinessTimeout bounds how long /ready will wait on any single
// dependency check, so a stalled dependency reports "not ready" instead of
// hanging the probe indefinitely.
const readinessTimeout = 3 * time.Second

const (
	depPostgres = "postgres"
	depRedis    = "redis"
	depMinio    = "minio"
)

// Pinger is satisfied by *database.Database and *cache.Cache. Depending on
// this narrow interface (rather than the whole application container) lets
// tests substitute a fake that fails on demand.
type Pinger interface {
	Ping(ctx context.Context) error
}

// HealthChecker is satisfied by storage.Storage.
type HealthChecker interface {
	HealthCheck(ctx context.Context) error
}

type readinessResponse struct {
	Status       string            `json:"status"`
	Dependencies map[string]string `json:"dependencies"`
}

// Readiness handles GET /ready: unlike Liveness, it verifies every critical
// dependency and reports which, if any, failed. It never includes
// connection strings, credentials, or raw driver errors in the response —
// only ok/error per dependency.
func Readiness(db, cache Pinger, store HealthChecker) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), readinessTimeout)
		defer cancel()

		deps := make(map[string]string, 3)
		ready := true

		checkDep := func(name string, check func(context.Context) error) {
			if err := check(ctx); err != nil {
				deps[name] = "error"
				ready = false
				return
			}
			deps[name] = "ok"
		}

		checkDep(depPostgres, db.Ping)
		checkDep(depRedis, cache.Ping)
		checkDep(depMinio, store.HealthCheck)

		status := http.StatusOK
		overall := "ready"
		if !ready {
			status = http.StatusServiceUnavailable
			overall = "not_ready"
		}

		c.JSON(status, readinessResponse{Status: overall, Dependencies: deps})
	}
}
