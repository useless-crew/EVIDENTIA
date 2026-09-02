package middleware

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"

	authpkg "evidentia/backend/internal/auth"
	"evidentia/backend/internal/authz"
	"evidentia/backend/internal/utils"
)

// fakeAuthorizer lets these tests drive HasPermission's outcome directly,
// so RequirePermission can be exercised without a real authz.Service or
// database — see Authorizer's doc comment.
type fakeAuthorizer struct {
	allowed bool
	err     error
}

func (f fakeAuthorizer) HasPermission(context.Context, authpkg.AuthenticatedUser, authz.Action) (bool, error) {
	return f.allowed, f.err
}

// newTestRouterWithPermission wires an optional pre-set authenticated user
// (nil means "Auth never ran / rejected the request") ahead of
// RequirePermission, so these tests can exercise both "no identity" and
// "identity present" without needing the real Auth middleware or a JWT.
func newTestRouterWithPermission(authorizer Authorizer, action authz.Action, user *authpkg.AuthenticatedUser) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		if user != nil {
			authpkg.SetAuthenticatedUser(c, *user)
		}
		c.Next()
	})
	r.GET("/protected", RequirePermission(authorizer, action), func(c *gin.Context) {
		c.Status(http.StatusOK)
	})
	return r
}

func TestRequirePermission_NoAuthenticatedUserReturns401(t *testing.T) {
	r := newTestRouterWithPermission(fakeAuthorizer{allowed: true}, authz.ActionCaseCreate, nil)

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Contains(t, rec.Body.String(), utils.CodeUnauthorized)
}

func TestRequirePermission_DeniedReturns403(t *testing.T) {
	user := authpkg.AuthenticatedUser{Roles: []string{"LAWYER"}}
	r := newTestRouterWithPermission(fakeAuthorizer{allowed: false}, authz.ActionCaseCreate, &user)

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.Contains(t, rec.Body.String(), utils.CodeForbidden)
}

func TestRequirePermission_AllowedReachesHandler(t *testing.T) {
	user := authpkg.AuthenticatedUser{Roles: []string{"ADMIN"}}
	r := newTestRouterWithPermission(fakeAuthorizer{allowed: true}, authz.ActionCaseCreate, &user)

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestRequirePermission_AuthorizerErrorReturns500NotAllowed(t *testing.T) {
	user := authpkg.AuthenticatedUser{Roles: []string{"ADMIN"}}
	r := newTestRouterWithPermission(fakeAuthorizer{err: errors.New("db unavailable")}, authz.ActionCaseCreate, &user)

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

// TestRequirePermission_IgnoresClientSuppliedHeaders is the RBAC-layer
// counterpart of the spoofing test already proven at the authentication
// layer (auth_middleware_test.go's TestAuthMiddleware_IgnoresClientSuppliedIdentityHeaders):
// the permission decision here comes only from the trusted, already-
// attached AuthenticatedUser, never from any client-supplied header —
// spoofing X-Role/X-Permission must not grant an action a fake authorizer
// has been told to deny.
func TestRequirePermission_IgnoresClientSuppliedHeaders(t *testing.T) {
	user := authpkg.AuthenticatedUser{Roles: []string{"LAWYER"}}
	r := newTestRouterWithPermission(fakeAuthorizer{allowed: false}, authz.ActionUserRole, &user)

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("X-Role", "ADMIN")
	req.Header.Set("X-Permission", "user:role")
	req.Header.Set("X-User-ID", "00000000-0000-0000-0000-000000000001")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusForbidden, rec.Code, "a spoofed X-Role/X-Permission/X-User-ID header must not grant access")
}
