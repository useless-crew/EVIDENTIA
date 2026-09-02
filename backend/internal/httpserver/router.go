// Package httpserver builds the Gin router and the *http.Server that serves
// it, wiring together middleware, health/readiness routes, and (in later
// systems) the versioned API.
package httpserver

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"evidentia/backend/internal/app"
	"evidentia/backend/internal/handlers/health"
	"evidentia/backend/internal/middleware"
	"evidentia/backend/internal/utils"
	"evidentia/backend/pkg/response"
)

// NewRouter builds the application's Gin engine: middleware stack, 404/405
// handlers, and health/readiness routes. Domain routes (auth, cases,
// documents, ...) are added by later systems under router.Group("/api/v1"),
// which is intentionally not created here since nothing is registered under
// it yet — see master prompt §39.
func NewRouter(a *app.App) *gin.Engine {
	if a.Config.App.IsProduction() {
		gin.SetMode(gin.ReleaseMode)
	} else {
		gin.SetMode(gin.DebugMode)
	}

	r := gin.New()
	r.HandleMethodNotAllowed = true

	r.Use(middleware.Recovery(a.Logger))
	r.Use(middleware.RequestID())
	r.Use(middleware.RequestLogger(a.Logger))
	r.Use(middleware.CORS(a.Config.CORS))
	r.Use(middleware.BodyLimit(a.Config.Server.MaxBodyBytes))

	r.NoRoute(func(c *gin.Context) {
		response.Error(c, http.StatusNotFound, utils.CodeNotFound, "The requested resource was not found")
	})
	r.NoMethod(func(c *gin.Context) {
		response.Error(c, http.StatusMethodNotAllowed, utils.CodeMethodNotAllowed, "Method not allowed for this resource")
	})

	r.GET("/health", health.Liveness(a.Config.App.Name, a.Config.App.Version))
	r.GET("/ready", health.Readiness(a.DB, a.Cache, a.Storage))

	return r
}
