package middleware

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"

	authpkg "evidentia/backend/internal/auth"
	"evidentia/backend/internal/authz"
	"evidentia/backend/internal/utils"
	"evidentia/backend/pkg/response"
)

// genericForbiddenMessage is the ONLY message this file's and
// abac_middleware.go's handlers ever return for a denial — never a
// specific reason ("LAWYER cannot create cases", "not a member of this
// case"), which would hand a client a map of the permission/ABAC model
// (master prompt §21/§30). The specific reason lives only in
// authz.Decision.Reason and the audit trail (authz.Service.recordDenied).
const genericForbiddenMessage = "You do not have permission to perform this action"

// Authorizer is the subset of *authz.Service RequirePermission depends
// on — declared here (the consumer), matching the IdentityResolver
// pattern auth_middleware.go already uses, so this middleware can be
// unit-tested with a fake and no database.
type Authorizer interface {
	HasPermission(ctx context.Context, user authpkg.AuthenticatedUser, action authz.Action) (bool, error)
}

// RequirePermission is the RBAC half of Evidentia's authorization
// middleware (see ARCHITECTURE.md's "Authorization Middleware -> RBAC ->
// ABAC" request flow). It must run AFTER Auth (which attaches the
// authenticated user to the context) and, for any resource-specific
// route, BEFORE the corresponding ABAC check (RequireCaseAccess/
// RequireDocumentAccess) — this check is deliberately cheap (no database
// resource lookup), so a request that fails here never pays for one
// (master prompt §17).
//
// Fails closed: a missing authenticated-user context (Auth not wired, or
// wired in the wrong order — a configuration defect, not a normal runtime
// path) is treated as unauthenticated, never as "unrestricted", and an
// authorizer error is treated as denied, never allowed.
func RequirePermission(authorizer Authorizer, action authz.Action) gin.HandlerFunc {
	return func(c *gin.Context) {
		user, ok := authpkg.CurrentUser(c)
		if !ok {
			response.Error(c, http.StatusUnauthorized, utils.CodeUnauthorized, genericUnauthorizedMessage)
			c.Abort()
			return
		}

		allowed, err := authorizer.HasPermission(c.Request.Context(), user, action)
		if err != nil {
			response.Error(c, http.StatusInternalServerError, utils.CodeInternal, "An unexpected error occurred")
			c.Abort()
			return
		}
		if !allowed {
			response.Error(c, http.StatusForbidden, utils.CodeForbidden, genericForbiddenMessage)
			c.Abort()
			return
		}
		c.Next()
	}
}
