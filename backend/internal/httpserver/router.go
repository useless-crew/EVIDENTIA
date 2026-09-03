// Package httpserver builds the Gin router and the *http.Server that serves
// it, wiring together middleware, health/readiness routes, and (in later
// systems) the versioned API.
package httpserver

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"evidentia/backend/internal/app"
	"evidentia/backend/internal/authz"
	authhandlers "evidentia/backend/internal/handlers/auth"
	casehandlers "evidentia/backend/internal/handlers/case"
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

	// Cases (System 5): every route requires authentication; POST/GET
	// (collection) additionally require the RBAC case:{create,read}
	// permission (middleware.RequirePermission), while the two
	// resource-scoped routes require the ABAC case-relationship check
	// (middleware.RequireCaseAccess) — see docs/API_ENDPOINTS.md's Cases
	// section and docs/SECURITY.md's Authorization section for the full
	// per-route mapping this mirrors exactly.
	authMW := middleware.Auth(a.JWTManager, a.AuthService, a.Logger)
	caseGroup := r.Group("/api/v1/cases")
	caseGroup.POST("", authMW, middleware.RequirePermission(a.AuthzService, authz.ActionCaseCreate), casehandlers.Create(a.CaseService))
	caseGroup.GET("", authMW, middleware.RequirePermission(a.AuthzService, authz.ActionCaseRead), casehandlers.List(a.CaseService))
	caseGroup.GET("/:id", authMW, middleware.RequireCaseAccess(a.AuthzService, authz.ActionCaseRead, "id"), casehandlers.Get(a.CaseService))
	caseGroup.PUT("/:id", authMW, middleware.RequireCaseAccess(a.AuthzService, authz.ActionCaseUpdate, "id"), casehandlers.Update(a.CaseService))

	// Document/audit/admin routes (internal/handlers/{document,audit,user})
	// remain not yet implemented — later systems' scope, not System 5's.
	// System 4 (internal/authz, internal/middleware.RequirePermission/
	// RequireCaseAccess/RequireDocumentAccess) already provides the
	// authorization primitives those routes will be guarded with, exactly
	// as used above for cases; see docs/API_ENDPOINTS.md for the full
	// intended per-route mapping.

	return r
}
