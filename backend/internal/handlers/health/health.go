// Package health implements the /health and /ready endpoints. Both return a
// deliberately flat, special-purpose JSON shape (not the general
// success/data/error envelope in pkg/response) — they are infrastructure
// probes consumed by orchestrators and monitoring, not typical API
// responses.
package health

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// livenessResponse is intentionally minimal: liveness answers only "is the
// process itself alive", never touching the database, cache, or storage.
type livenessResponse struct {
	Status  string `json:"status"`
	Service string `json:"service"`
	Version string `json:"version"`
}

// Liveness handles GET /health. It takes only the values it needs (not the
// whole application container), so it has no dependency to fake in tests.
func Liveness(service, version string) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.JSON(http.StatusOK, livenessResponse{
			Status:  "ok",
			Service: service,
			Version: version,
		})
	}
}
