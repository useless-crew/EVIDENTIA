// Package httpserver builds the Gin router and the *http.Server that serves
// it, wiring together middleware, health/readiness routes, and (in later
// systems) the versioned API.
package httpserver

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"evidentia/backend/internal/app"
	authhandlers "evidentia/backend/internal/handlers/auth"
	"evidentia/backend/internal/handlers/health"
	"evidentia/backend/internal/middleware"
	"evidentia/backend/internal/utils"
	"evidentia/backend/pkg/response"
)

// NewRouter builds the application's Gin engine: middleware stack, 404/405
// handlers, health/readiness routes, and the /api/v1/auth routes (System 3).
// Case/document/... routes are added by later systems under the same
// /api/v1 group — see master prompt §39.
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

	// POST /auth/login and /auth/refresh are deliberately public (master
	// prompt §56) — the credential/token presented in the request body IS
	// the authentication, so no Authorization header is required to reach
	// them. /auth/logout, by contrast, requires a valid access token (see
	// master prompt §56 and internal/handlers/auth/logout.go's doc
	// comment for why that specific choice was made).
	authGroup := r.Group("/api/v1/auth")
	authGroup.POST("/login", authhandlers.Login(a.AuthService))
	authGroup.POST("/refresh", authhandlers.Refresh(a.AuthService))
	authGroup.POST("/logout", middleware.Auth(a.JWTManager, a.AuthService, a.Logger), authhandlers.Logout(a.AuthService))

	// Case/document/audit/admin routes (internal/handlers/{case,document,
	// audit,user}) are not yet implemented — see those packages' TODOs;
	// their business logic is a later system's scope, not System 4's.
	// System 4 (internal/authz, internal/middleware.RequirePermission/
	// RequireCaseAccess/RequireDocumentAccess) provides the authorization
	// primitives those routes will be guarded with once they exist, e.g.:
	//
	//   caseGroup.POST("", middleware.Auth(...), middleware.RequirePermission(a.AuthzService, authz.ActionCaseCreate), handler)
	//   caseGroup.GET("/:id", middleware.Auth(...), middleware.RequireCaseAccess(a.AuthzService, authz.ActionCaseRead, "id"), handler)
	//
	// See docs/API_ENDPOINTS.md for the full intended per-route mapping.

	return r
}
