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
	documenthandlers "evidentia/backend/internal/handlers/document"
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
	// BodyLimit is deliberately NOT applied engine-wide: document upload
	// routes (System 6) need a much larger limit than JSON-bodied routes,
	// and two http.MaxBytesReader wrappings on the same request compose by
	// taking the SMALLER of the two — see body_limit_middleware.go's doc
	// comment. Each route group below applies the limit appropriate to it.

	r.NoRoute(func(c *gin.Context) {
		response.Error(c, http.StatusNotFound, utils.CodeNotFound, "The requested resource was not found")
	})
	r.NoMethod(func(c *gin.Context) {
		response.Error(c, http.StatusMethodNotAllowed, utils.CodeMethodNotAllowed, "Method not allowed for this resource")
	})

	r.GET("/health", health.Liveness(a.Config.App.Name, a.Config.App.Version))
	r.GET("/ready", health.Readiness(a.DB, a.Cache, a.Storage))

	jsonBodyLimit := middleware.BodyLimit(a.Config.Server.MaxBodyBytes)
	authMW := middleware.Auth(a.JWTManager, a.AuthService, a.Logger)

	// POST /auth/login and /auth/refresh are deliberately public (master
	// prompt §56) — the credential/token presented in the request body IS
	// the authentication, so no Authorization header is required to reach
	// them. /auth/logout, by contrast, requires a valid access token (see
	// master prompt §56 and internal/handlers/auth/logout.go's doc
	// comment for why that specific choice was made).
	authGroup := r.Group("/api/v1/auth")
	authGroup.Use(jsonBodyLimit)
	authGroup.POST("/login", authhandlers.Login(a.AuthService))
	authGroup.POST("/refresh", authhandlers.Refresh(a.AuthService))
	authGroup.POST("/logout", authMW, authhandlers.Logout(a.AuthService))

	// Cases (System 5): every route requires authentication; POST/GET
	// (collection) additionally require the RBAC case:{create,read}
	// permission (middleware.RequirePermission), while the two
	// resource-scoped routes require the ABAC case-relationship check
	// (middleware.RequireCaseAccess) — see docs/API_ENDPOINTS.md's Cases
	// section and docs/SECURITY.md's Authorization section for the full
	// per-route mapping this mirrors exactly.
	caseGroup := r.Group("/api/v1/cases")
	caseGroup.Use(jsonBodyLimit)
	caseGroup.POST("", authMW, middleware.RequirePermission(a.AuthzService, authz.ActionCaseCreate), casehandlers.Create(a.CaseService))
	caseGroup.GET("", authMW, middleware.RequirePermission(a.AuthzService, authz.ActionCaseRead), casehandlers.List(a.CaseService))
	caseGroup.GET("/:id", authMW, middleware.RequireCaseAccess(a.AuthzService, authz.ActionCaseRead, "id"), casehandlers.Get(a.CaseService))
	caseGroup.PUT("/:id", authMW, middleware.RequireCaseAccess(a.AuthzService, authz.ActionCaseUpdate, "id"), casehandlers.Update(a.CaseService))

	// Documents (System 6): upload is nested under its case
	// (/api/v1/cases/:id/documents — :id is the CASE id) and gated by the
	// SAME RequireCaseAccess ABAC check as the case routes above, with
	// authz.ActionDocumentUpload as the permission — this is master prompt
	// §10's "RBAC document:upload permission AND case access" in one call.
	// It gets its own, much larger body limit
	// (DocumentsConfig.MaxUploadSize), never the JSON routes' limit above.
	// Download (/api/v1/documents/:id/download — :id is the DOCUMENT id)
	// uses RequireDocumentAccess, which resolves the document's case
	// internally; it needs no body limit (GET, no body).
	uploadBodyLimit := middleware.BodyLimit(a.Config.Documents.MaxUploadSize)
	r.POST("/api/v1/cases/:id/documents", authMW, middleware.RequireCaseAccess(a.AuthzService, authz.ActionDocumentUpload, "id"), uploadBodyLimit, documenthandlers.Upload(a.DocumentService))
	r.GET("/api/v1/documents/:id/download", authMW, middleware.RequireDocumentAccess(a.AuthzService, authz.ActionDocumentDownload, "id"), documenthandlers.Download(a.DocumentService))

	// Audit/admin routes (internal/handlers/{audit,user}) remain not yet
	// implemented — later systems' scope. System 4's authorization
	// primitives are already available for whichever system adds them; see
	// docs/API_ENDPOINTS.md for the full intended per-route mapping.

	return r
}
