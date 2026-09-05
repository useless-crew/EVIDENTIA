// Package httpserver builds the Gin router and the *http.Server that serves
// it, wiring together middleware, health/readiness routes, and (in later
// systems) the versioned API.
package httpserver

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"evidentia/backend/internal/app"
	"evidentia/backend/internal/authz"
	audithandlers "evidentia/backend/internal/handlers/audit"
	authhandlers "evidentia/backend/internal/handlers/auth"
	casehandlers "evidentia/backend/internal/handlers/case"
	documenthandlers "evidentia/backend/internal/handlers/document"
	"evidentia/backend/internal/handlers/health"
	sharedhandlers "evidentia/backend/internal/handlers/shared"
	userhandlers "evidentia/backend/internal/handlers/user"
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

	// Verification & compliance certificates (System 7): both routes are
	// document-scoped (:id is the DOCUMENT id) and use RequireDocumentAccess,
	// same as download above. Verify needs no body limit (POST with no
	// request body). Certificate generation's ADDITIONAL certificate:create
	// check (beyond the certificate:read gate here) happens inside
	// CertificateService itself — see its doc comment for why a second
	// route-level middleware isn't the right place for that distinction.
	r.POST("/api/v1/documents/:id/verify", authMW, middleware.RequireDocumentAccess(a.AuthzService, authz.ActionDocumentVerify, "id"), documenthandlers.Verify(a.DocumentService))
	r.GET("/api/v1/documents/:id/certificate", authMW, middleware.RequireDocumentAccess(a.AuthzService, authz.ActionCertificateRead, "id"), documenthandlers.Certificate(a.CertificateService))

	// Redaction: document-scoped (:id is the SOURCE document ID), same
	// RequireDocumentAccess pattern as verify/certificate above, with
	// authz.ActionDocumentRedact. Takes a small JSON body (reason +
	// regions), so — unlike verify (no body) — it needs jsonBodyLimit, the
	// same limit auth/case/admin routes already share (a redaction
	// request's region list is nowhere near upload-sized).
	r.POST("/api/v1/documents/:id/redact", authMW, middleware.RequireDocumentAccess(a.AuthzService, authz.ActionDocumentRedact, "id"), jsonBodyLimit, documenthandlers.Redact(a.DocumentService))

	// Secure document sharing & access delegation: create/list/revoke are
	// all document-scoped (:id is the SOURCE document) and gated by the
	// SAME authz.ActionDocumentShare permission — "authorized to manage
	// this document's sharing", not narrowed to only the share's original
	// creator (see internal/service.ShareService.RevokeShare's doc
	// comment). Create takes a small JSON body, so it gets jsonBodyLimit
	// like redact does; list/revoke take no body.
	r.POST("/api/v1/documents/:id/share", authMW, middleware.RequireDocumentAccess(a.AuthzService, authz.ActionDocumentShare, "id"), jsonBodyLimit, documenthandlers.Share(a.ShareService))
	r.GET("/api/v1/documents/:id/shares", authMW, middleware.RequireDocumentAccess(a.AuthzService, authz.ActionDocumentShare, "id"), documenthandlers.ListShares(a.ShareService))
	r.POST("/api/v1/documents/:id/shares/:shareId/revoke", authMW, middleware.RequireDocumentAccess(a.AuthzService, authz.ActionDocumentShare, "id"), documenthandlers.RevokeShare(a.ShareService))

	// "Shared With Me" (master prompt §59): a top-level route, not nested
	// under /documents/:id — it is not scoped to any single document.
	// Authenticated-only; service.ShareService.ListSharedWithMe's own
	// query (backed by documents_select's RLS delegated-access branch)
	// is the only authorization this needs.
	r.GET("/api/v1/shared/documents", authMW, sharedhandlers.SharedWithMe(a.ShareService))

	// Share-recipient search (master prompt §38/§48): authenticated-only,
	// deliberately NOT the admin-only user:read permission — see
	// internal/handlers/user.Search's doc comment.
	r.GET("/api/v1/users/search", authMW, userhandlers.Search(a.ShareService))

	// Admin user management (System 8): every route requires
	// authentication; POST/GET/GET-by-id/PUT/status/password additionally
	// require the matching RBAC user:* permission
	// (middleware.RequirePermission) — see docs/API_ENDPOINTS.md's Admin
	// section. The role route is the one exception: its authorization is
	// entirely UserService.UpdateRole's call to
	// authz.Service.CanModifyUserRole (RBAC user:role PLUS the hard block
	// on self-role-modification), exactly as that doc documents, so no
	// separate RequirePermission wraps it here.
	adminGroup := r.Group("/api/v1/admin")
	adminGroup.Use(jsonBodyLimit)
	adminGroup.POST("/users", authMW, middleware.RequirePermission(a.AuthzService, authz.ActionUserCreate), userhandlers.Create(a.UserService))
	adminGroup.GET("/users", authMW, middleware.RequirePermission(a.AuthzService, authz.ActionUserRead), userhandlers.List(a.UserService))
	adminGroup.GET("/users/:id", authMW, middleware.RequirePermission(a.AuthzService, authz.ActionUserRead), userhandlers.Get(a.UserService))
	adminGroup.PUT("/users/:id", authMW, middleware.RequirePermission(a.AuthzService, authz.ActionUserUpdate), userhandlers.Update(a.UserService))
	adminGroup.PUT("/users/:id/role", authMW, userhandlers.UpdateRole(a.UserService))
	adminGroup.PUT("/users/:id/status", authMW, middleware.RequirePermission(a.AuthzService, authz.ActionUserDeactivate), userhandlers.UpdateStatus(a.UserService))
	adminGroup.PUT("/users/:id/password", authMW, middleware.RequirePermission(a.AuthzService, authz.ActionUserUpdate), userhandlers.ResetPassword(a.UserService))
	adminGroup.GET("/roles", authMW, userhandlers.ListRoles(a.UserService))

	// Self-profile: any authenticated user, regardless of role, may view
	// their own record — see handlers/user/profile.go's doc comment for
	// why this deliberately does not go through the same user:read gate
	// GET /admin/users/:id does.
	r.GET("/api/v1/users/me", authMW, userhandlers.Profile(a.UserService))

	// Audit trail: GET /audit is a filtered LISTING with no single case/
	// document ID in its URL (like GET /cases), so it is gated by
	// RequirePermission (RBAC only) here — row-level visibility beyond
	// that is PostgreSQL RLS's job (audit_log_select), re-checked
	// independently by AuditService.List.
	r.GET("/api/v1/audit", authMW, middleware.RequirePermission(a.AuthzService, authz.ActionAuditRead), audithandlers.List(a.AuditService))

	// Audit-chain verification & integrity dashboard (System 11): every
	// route below is audit:verify (ADMIN-only per the seed data) —
	// verifying/inspecting the GLOBAL chain only makes sense against the
	// complete, unfiltered view, exactly like System 10's original
	// synchronous POST /audit/verify-chain already required; see
	// docs/AUDIT_CHAIN.md. None of these take a JSON body (POST has none,
	// the rest are GETs), so none get jsonBodyLimit.
	r.POST("/api/v1/audit/verify-chain", authMW, middleware.RequirePermission(a.AuthzService, authz.ActionAuditVerify), audithandlers.VerifyChain(a.AuditService))
	r.GET("/api/v1/audit/verify-chain/:verificationId", authMW, middleware.RequirePermission(a.AuthzService, authz.ActionAuditVerify), audithandlers.Status(a.AuditService))
	r.GET("/api/v1/audit/verify-chain/:verificationId/events", authMW, middleware.RequirePermission(a.AuthzService, authz.ActionAuditVerify), audithandlers.Events(a.AuditService))
	r.GET("/api/v1/audit/verifications", authMW, middleware.RequirePermission(a.AuthzService, authz.ActionAuditVerify), audithandlers.History(a.AuditService))
	r.GET("/api/v1/audit/integrity", authMW, middleware.RequirePermission(a.AuthzService, authz.ActionAuditVerify), audithandlers.Integrity(a.AuditService))

	return r
}
