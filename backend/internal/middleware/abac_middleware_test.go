package middleware

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"

	authpkg "evidentia/backend/internal/auth"
	"evidentia/backend/internal/authz"
	"evidentia/backend/internal/utils"
)

type fakeCaseAuthorizer struct {
	decision authz.Decision
	err      error
}

func (f fakeCaseAuthorizer) CanAccessCase(context.Context, authpkg.AuthenticatedUser, uuid.UUID, authz.Action) (authz.Decision, error) {
	return f.decision, f.err
}

type fakeDocumentAuthorizer struct {
	decision authz.Decision
	err      error
}

func (f fakeDocumentAuthorizer) CanAccessDocument(context.Context, authpkg.AuthenticatedUser, uuid.UUID, authz.Action) (authz.Decision, error) {
	return f.decision, f.err
}

func newTestRouterWithCaseAccess(authorizer CaseAuthorizer, user *authpkg.AuthenticatedUser) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		if user != nil {
			authpkg.SetAuthenticatedUser(c, *user)
		}
		c.Next()
	})
	r.GET("/cases/:id", RequireCaseAccess(authorizer, authz.ActionCaseRead, "id"), func(c *gin.Context) {
		c.Status(http.StatusOK)
	})
	return r
}

func TestRequireCaseAccess_NoAuthenticatedUserReturns401(t *testing.T) {
	r := newTestRouterWithCaseAccess(fakeCaseAuthorizer{decision: authz.Decision{Allowed: true}}, nil)

	req := httptest.NewRequest(http.MethodGet, "/cases/"+uuid.New().String(), nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestRequireCaseAccess_MalformedIDDeniedNotValidationError(t *testing.T) {
	user := authpkg.AuthenticatedUser{Roles: []string{"LAWYER"}}
	// The fake would ALLOW if ever consulted — proving a malformed ID is
	// rejected before the authorizer is even called, and that it produces
	// the same 403 a real denial would, not a distinguishable 400.
	r := newTestRouterWithCaseAccess(fakeCaseAuthorizer{decision: authz.Decision{Allowed: true}}, &user)

	req := httptest.NewRequest(http.MethodGet, "/cases/not-a-uuid", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestRequireCaseAccess_DeniedReturns403(t *testing.T) {
	user := authpkg.AuthenticatedUser{Roles: []string{"LAWYER"}}
	r := newTestRouterWithCaseAccess(fakeCaseAuthorizer{decision: authz.Decision{Allowed: false}}, &user)

	req := httptest.NewRequest(http.MethodGet, "/cases/"+uuid.New().String(), nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.Contains(t, rec.Body.String(), utils.CodeForbidden)
}

// TestRequireCaseAccess_UnauthorizedAndNonexistentLookIdentical proves the
// anti-enumeration property directly: a "the case doesn't exist" decision
// and a "the case exists but you have no relationship to it" decision
// must produce byte-for-byte identical HTTP responses, so a client can
// never distinguish the two (master prompt §21/§25).
func TestRequireCaseAccess_UnauthorizedAndNonexistentLookIdentical(t *testing.T) {
	user := authpkg.AuthenticatedUser{Roles: []string{"LAWYER"}}

	rNotFound := newTestRouterWithCaseAccess(fakeCaseAuthorizer{decision: authz.Decision{Allowed: false, Reason: "not_found_or_no_relationship"}}, &user)
	reqNotFound := httptest.NewRequest(http.MethodGet, "/cases/"+uuid.New().String(), nil)
	recNotFound := httptest.NewRecorder()
	rNotFound.ServeHTTP(recNotFound, reqNotFound)

	rUnrelated := newTestRouterWithCaseAccess(fakeCaseAuthorizer{decision: authz.Decision{Allowed: false, Reason: "not_case_member"}}, &user)
	reqUnrelated := httptest.NewRequest(http.MethodGet, "/cases/"+uuid.New().String(), nil)
	recUnrelated := httptest.NewRecorder()
	rUnrelated.ServeHTTP(recUnrelated, reqUnrelated)

	assert.Equal(t, recNotFound.Code, recUnrelated.Code)
	assert.Equal(t, recNotFound.Body.String(), recUnrelated.Body.String())
}

func TestRequireCaseAccess_AllowedReachesHandler(t *testing.T) {
	user := authpkg.AuthenticatedUser{Roles: []string{"LAWYER"}}
	r := newTestRouterWithCaseAccess(fakeCaseAuthorizer{decision: authz.Decision{Allowed: true}}, &user)

	req := httptest.NewRequest(http.MethodGet, "/cases/"+uuid.New().String(), nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestRequireCaseAccess_AuthorizerErrorReturns500(t *testing.T) {
	user := authpkg.AuthenticatedUser{Roles: []string{"LAWYER"}}
	r := newTestRouterWithCaseAccess(fakeCaseAuthorizer{err: errors.New("authz backend unavailable")}, &user)

	req := httptest.NewRequest(http.MethodGet, "/cases/"+uuid.New().String(), nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestRequireDocumentAccess_DeniedReturns403(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	user := authpkg.AuthenticatedUser{Roles: []string{"FORENSICS"}}
	r.Use(func(c *gin.Context) {
		authpkg.SetAuthenticatedUser(c, user)
		c.Next()
	})
	r.GET("/documents/:id/download", RequireDocumentAccess(fakeDocumentAuthorizer{decision: authz.Decision{Allowed: false}}, authz.ActionDocumentDownload, "id"), func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/documents/"+uuid.New().String()+"/download", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestRequireDocumentAccess_AllowedReachesHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	user := authpkg.AuthenticatedUser{Roles: []string{"FORENSICS"}}
	r.Use(func(c *gin.Context) {
		authpkg.SetAuthenticatedUser(c, user)
		c.Next()
	})
	r.GET("/documents/:id/download", RequireDocumentAccess(fakeDocumentAuthorizer{decision: authz.Decision{Allowed: true}}, authz.ActionDocumentDownload, "id"), func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/documents/"+uuid.New().String()+"/download", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestRequireDocumentAccess_MalformedIDDenied(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	user := authpkg.AuthenticatedUser{Roles: []string{"FORENSICS"}}
	r.Use(func(c *gin.Context) {
		authpkg.SetAuthenticatedUser(c, user)
		c.Next()
	})
	r.GET("/documents/:id/download", RequireDocumentAccess(fakeDocumentAuthorizer{decision: authz.Decision{Allowed: true}}, authz.ActionDocumentDownload, "id"), func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/documents/not-a-uuid/download", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusForbidden, rec.Code)
}
