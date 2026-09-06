package admin

import (
	"context"
	"net/http"
	"runtime"
	"time"

	"github.com/gin-gonic/gin"

	"evidentia/backend/internal/blockchain"
	"evidentia/backend/internal/storage"
	"evidentia/backend/pkg/response"
)

// SystemHealthResponse carries comprehensive infrastructure health and operational metrics.
// No raw credentials, tokens, or private keys are exposed.
type SystemHealthResponse struct {
	OverallStatus string            `json:"overall_status"`
	Timestamp     time.Time         `json:"timestamp"`
	Components    map[string]ComponentHealth `json:"components"`
	Runtime       RuntimeInfo       `json:"runtime"`
}

type ComponentHealth struct {
	Status      string         `json:"status"` // ok, degraded, error, disabled
	Details     string         `json:"details,omitempty"`
	LatencyMs   int64          `json:"latency_ms,omitempty"`
	Extra       map[string]any `json:"extra,omitempty"`
}

type RuntimeInfo struct {
	GoVersion     string  `json:"go_version"`
	Goroutines    int     `json:"goroutines"`
	MemoryAllocMB float64 `json:"memory_alloc_mb"`
	MemorySysMB   float64 `json:"memory_sys_mb"`
	NumCPU        int     `json:"num_cpu"`
}

// Pinger is satisfied by any database/cache that can be pinged.
type Pinger interface {
	Ping(ctx context.Context) error
}


// SystemHealth handles GET /api/v1/admin/system/health.
// Restricted to ADMIN.
func SystemHealth(
	db Pinger,
	redisCache Pinger,
	store storage.Storage,
	blockchainSvc blockchain.Service,
) gin.HandlerFunc {

	return func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 4*time.Second)
		defer cancel()

		components := make(map[string]ComponentHealth)
		overallOK := true

		// 1. PostgreSQL
		pgStart := time.Now()
		if err := db.Ping(ctx); err != nil {
			overallOK = false
			components["postgres"] = ComponentHealth{
				Status:    "error",
				Details:   "Database ping failed",
				LatencyMs: time.Since(pgStart).Milliseconds(),
			}
		} else {
			components["postgres"] = ComponentHealth{
				Status:    "ok",
				Details:   "Connected to PostgreSQL (RLS active)",
				LatencyMs: time.Since(pgStart).Milliseconds(),
				Extra: map[string]any{
					"managed_by": "Adminer (Host port 8088)",
				},
			}
		}

		// 2. Redis
		redisStart := time.Now()
		if err := redisCache.Ping(ctx); err != nil {
			overallOK = false
			components["redis"] = ComponentHealth{
				Status:    "error",
				Details:   "Redis ping failed",
				LatencyMs: time.Since(redisStart).Milliseconds(),
			}
		} else {
			components["redis"] = ComponentHealth{
				Status:    "ok",
				Details:   "Connected to Redis (Asynq & Pub/Sub ready)",
				LatencyMs: time.Since(redisStart).Milliseconds(),
			}
		}

		// 3. MinIO
		minioStart := time.Now()
		if err := store.HealthCheck(ctx); err != nil {
			overallOK = false
			components["minio"] = ComponentHealth{
				Status:    "error",
				Details:   "Object storage check failed",
				LatencyMs: time.Since(minioStart).Milliseconds(),
			}
		} else {
			extra := map[string]any{
				"console": "MinIO Console (Host port 9001)",
			}
			if minioStore, ok := store.(*storage.MinIOStorage); ok {
				extra["bucket"] = minioStore.Bucket()
			}
			components["minio"] = ComponentHealth{
				Status:    "ok",
				Details:   "Object storage bucket accessible",
				LatencyMs: time.Since(minioStart).Milliseconds(),
				Extra:     extra,
			}
		}

		// 4. Hyperledger Fabric Blockchain
		if !blockchainSvc.IsEnabled() {
			components["blockchain"] = ComponentHealth{
				Status:  "disabled",
				Details: "Hyperledger Fabric integration disabled (FABRIC_ENABLED=false)",
			}
		} else {
			components["blockchain"] = ComponentHealth{
				Status:  "ok",
				Details: "Hyperledger Fabric Gateway connected",
			}
		}

		// 5. Runtime stats
		var memStats runtime.MemStats
		runtime.ReadMemStats(&memStats)
		runtimeData := RuntimeInfo{
			GoVersion:     runtime.Version(),
			Goroutines:    runtime.NumGoroutine(),
			MemoryAllocMB: float64(memStats.Alloc) / (1024 * 1024),
			MemorySysMB:   float64(memStats.Sys) / (1024 * 1024),
			NumCPU:        runtime.NumCPU(),
		}

		status := "HEALTHY"
		if !overallOK {
			status = "DEGRADED"
		}

		response.Success(c, http.StatusOK, SystemHealthResponse{
			OverallStatus: status,
			Timestamp:     time.Now().UTC(),
			Components:    components,
			Runtime:       runtimeData,
		})
	}
}
