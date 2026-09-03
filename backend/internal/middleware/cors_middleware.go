package middleware

import (
	"net/http"
	"slices"
	"strings"

	"github.com/gin-gonic/gin"

	"evidentia/backend/internal/config"
)

// CORS builds a CORS middleware from the application's CORSConfig. Origins,
// methods, and headers are all driven by configuration — nothing here is
// hardcoded to "*", so production deployments must explicitly configure
// allowed origins (see config.validate, which rejects "*" when
// APP_ENV=production).
func CORS(cfg config.CORSConfig) gin.HandlerFunc {
	allowAllOrigins := slices.Contains(cfg.AllowedOrigins, "*")
	methods := strings.Join(cfg.AllowedMethods, ", ")
	headers := strings.Join(cfg.AllowedHeaders, ", ")

	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		if origin != "" && (allowAllOrigins || slices.Contains(cfg.AllowedOrigins, origin)) {
			if allowAllOrigins && !cfg.AllowCredentials {
				c.Writer.Header().Set("Access-Control-Allow-Origin", "*")
			} else {
				c.Writer.Header().Set("Access-Control-Allow-Origin", origin)
				c.Writer.Header().Set("Vary", "Origin")
			}
			if cfg.AllowCredentials {
				c.Writer.Header().Set("Access-Control-Allow-Credentials", "true")
			}
		}

		c.Writer.Header().Set("Access-Control-Allow-Methods", methods)
		c.Writer.Header().Set("Access-Control-Allow-Headers", headers)
		// Content-Disposition carries the server-suggested filename on
		// GET /documents/:id/download (internal/handlers/document/download.go)
		// — browsers hide response headers from cross-origin JS unless
		// explicitly exposed, so a frontend on a different origin/port
		// (e.g. the Angular dev server) could not otherwise read it to
		// name a saved file. This exposes exactly one read-only response
		// header; it does not widen which origins/methods/request headers
		// are allowed.
		c.Writer.Header().Set("Access-Control-Expose-Headers", "Content-Disposition")

		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}

		c.Next()
	}
}
