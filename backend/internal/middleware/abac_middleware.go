package middleware

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	authpkg "evidentia/backend/internal/auth"
	"evidentia/backend/internal/authz"
	"evidentia/backend/internal/utils"
	"evidentia/backend/pkg/response"
)

// CaseAuthorizer is the subset of *authz.Service RequireCaseAccess depends
// on — declared here for the same testability reason as Authorizer.
type CaseAuthorizer interface {
	CanAccessCase(ctx context.Context, user authpkg.AuthenticatedUser, caseID uuid.UUID, action authz.Action) (authz.Decision, error)
}

// DocumentAuthorizer is the subset of *authz.Service RequireDocumentAccess
// depends on.
type DocumentAuthorizer interface {
	CanAccessDocument(ctx context.Context, user authpkg.AuthenticatedUser, documentID uuid.UUID, action authz.Action) (authz.Decision, error)
}

// RequireCaseAccess is the ABAC half of the authorization middleware for
// any route with a case-ID path parameter (e.g. GET/PUT /cases/:id). It
// runs after Auth and is typically paired with RequirePermission for the
// same action on routes that need both — see docs/SECURITY.md's
// Authorization section for the full composition.
//
// A malformed/unparseable ID is denied exactly like a well-formed but
// unauthorized one — both resolve to the identical generic 403 (master
// prompt §2: "missing, malformed, ambiguous ... DENY"), never a
// distinguishable response that could help an attacker probe ID formats
// or enumerate resources (master prompt §21/§25).
func RequireCaseAccess(authorizer CaseAuthorizer, action authz.Action, paramName string) gin.HandlerFunc {
	return func(c *gin.Context) {
		user, ok := authpkg.CurrentUser(c)
		if !ok {
			response.Error(c, http.StatusUnauthorized, utils.CodeUnauthorized, genericUnauthorizedMessage)
			c.Abort()
			return
		}

		caseID, err := uuid.Parse(c.Param(paramName))
		if err != nil {
			response.Error(c, http.StatusForbidden, utils.CodeForbidden, genericForbiddenMessage)
			c.Abort()
			return
		}

		decision, err := authorizer.CanAccessCase(c.Request.Context(), user, caseID, action)
		if err != nil {
			response.Error(c, http.StatusInternalServerError, utils.CodeInternal, "An unexpected error occurred")
			c.Abort()
			return
		}
		if !decision.Allowed {
			response.Error(c, http.StatusForbidden, utils.CodeForbidden, genericForbiddenMessage)
			c.Abort()
			return
		}
		c.Next()
	}
}

// RequireDocumentAccess is RequireCaseAccess's document-scoped
// counterpart — see its doc comment for the shared design rationale.
func RequireDocumentAccess(authorizer DocumentAuthorizer, action authz.Action, paramName string) gin.HandlerFunc {
	return func(c *gin.Context) {
		user, ok := authpkg.CurrentUser(c)
		if !ok {
			response.Error(c, http.StatusUnauthorized, utils.CodeUnauthorized, genericUnauthorizedMessage)
			c.Abort()
			return
		}

		documentID, err := uuid.Parse(c.Param(paramName))
		if err != nil {
			response.Error(c, http.StatusForbidden, utils.CodeForbidden, genericForbiddenMessage)
			c.Abort()
			return
		}

		decision, err := authorizer.CanAccessDocument(c.Request.Context(), user, documentID, action)
		if err != nil {
			response.Error(c, http.StatusInternalServerError, utils.CodeInternal, "An unexpected error occurred")
			c.Abort()
			return
		}
		if !decision.Allowed {
			response.Error(c, http.StatusForbidden, utils.CodeForbidden, genericForbiddenMessage)
			c.Abort()
			return
		}
		c.Next()
	}
}
