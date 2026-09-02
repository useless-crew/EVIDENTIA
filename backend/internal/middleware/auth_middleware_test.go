package middleware

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	authpkg "evidentia/backend/internal/auth"
	"evidentia/backend/internal/utils"
)

// fakeResolver lets these tests drive ResolveIdentity's outcome directly,
// so the full JWT + account-status flow can be exercised without a real
// database — see IdentityResolver's doc comment.
type fakeResolver struct {
	identity authpkg.AuthenticatedUser
	err      error
}

func (f fakeResolver) ResolveIdentity(context.Context, uuid.UUID) (authpkg.AuthenticatedUser, error) {
	return f.identity, f.err
}

// newTestRouterWithAuth returns the router plus a pointer to a slot the
// protected handler fills in via *seen = u (not seen = &u — a plain
// reassignment would only rebind this function's local variable, never
// visible to the caller, since ServeHTTP runs after this function has
// already returned). seen stays its zero value if the handler is never
// reached (request rejected by the middleware) — callers check rec.Code
// to distinguish that case rather than a nil check, since seen itself is
// always a valid, non-nil pointer.
func newTestRouterWithAuth(jwtManager *authpkg.JWTManager, resolver IdentityResolver) (*gin.Engine, *authpkg.AuthenticatedUser) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	seen := new(authpkg.AuthenticatedUser)
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	r.Use(Auth(jwtManager, resolver, logger))
	r.GET("/protected", func(c *gin.Context) {
		if u, ok := authpkg.CurrentUser(c); ok {
			*seen = u
		}
		c.Status(http.StatusOK)
	})
	return r, seen
}

func TestAuthMiddleware_MissingAuthorizationHeaderReturns401(t *testing.T) {
	m := authpkg.NewJWTManager("test-signing-key-at-least-32-characters-long", "evidentia-api", "evidentia-client", 15*time.Minute)
	r, _ := newTestRouterWithAuth(m, fakeResolver{})

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Contains(t, rec.Body.String(), utils.CodeUnauthorized)
}

func TestAuthMiddleware_MalformedAuthorizationHeaderReturns401(t *testing.T) {
	m := authpkg.NewJWTManager("test-signing-key-at-least-32-characters-long", "evidentia-api", "evidentia-client", 15*time.Minute)
	r, _ := newTestRouterWithAuth(m, fakeResolver{})

	for _, header := range []string{"Token abc", "Bearer", "Bearer ", "bearer abc"} {
		req := httptest.NewRequest(http.MethodGet, "/protected", nil)
		req.Header.Set("Authorization", header)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusUnauthorized, rec.Code, "header %q should be rejected", header)
	}
}

func TestAuthMiddleware_InvalidJWTReturns401(t *testing.T) {
	m := authpkg.NewJWTManager("test-signing-key-at-least-32-characters-long", "evidentia-api", "evidentia-client", 15*time.Minute)
	r, _ := newTestRouterWithAuth(m, fakeResolver{})

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer not-a-real-token")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestAuthMiddleware_ExpiredJWTReturns401(t *testing.T) {
	m := authpkg.NewJWTManager("test-signing-key-at-least-32-characters-long", "evidentia-api", "evidentia-client", -1*time.Minute)
	token, _, err := m.CreateAccessToken(uuid.New(), "LAWYER")
	require.NoError(t, err)

	r, _ := newTestRouterWithAuth(m, fakeResolver{})
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestAuthMiddleware_ValidJWTAttachesAuthenticatedContext(t *testing.T) {
	m := authpkg.NewJWTManager("test-signing-key-at-least-32-characters-long", "evidentia-api", "evidentia-client", 15*time.Minute)
	userID := uuid.New()
	token, _, err := m.CreateAccessToken(userID, "LAWYER")
	require.NoError(t, err)

	identity := authpkg.AuthenticatedUser{ID: userID, Email: "officer@example.com", Roles: []string{"LAWYER"}}
	r, seen := newTestRouterWithAuth(m, fakeResolver{identity: identity})

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, userID, seen.ID)
	assert.Equal(t, "officer@example.com", seen.Email)
	assert.Equal(t, []string{"LAWYER"}, seen.Roles)
}

// TestAuthMiddleware_RejectsWhenIdentityCannotBeResolved is the middleware
// half of master prompt §62 (user deactivation): even with a perfectly
// valid, unexpired JWT, a resolver reporting the account is no longer
// usable (deactivated, deleted) must reject the request.
func TestAuthMiddleware_RejectsWhenIdentityCannotBeResolved(t *testing.T) {
	m := authpkg.NewJWTManager("test-signing-key-at-least-32-characters-long", "evidentia-api", "evidentia-client", 15*time.Minute)
	token, _, err := m.CreateAccessToken(uuid.New(), "LAWYER")
	require.NoError(t, err)

	r, _ := newTestRouterWithAuth(m, fakeResolver{err: utils.ErrUnauthorized("Authentication required")})
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

// TestAuthMiddleware_IgnoresClientSuppliedIdentityHeaders is the explicit
// spoofing test from master prompt §63/§31: identity comes ONLY from a
// validated JWT plus the resolver, never from any client-supplied header.
func TestAuthMiddleware_IgnoresClientSuppliedIdentityHeaders(t *testing.T) {
	m := authpkg.NewJWTManager("test-signing-key-at-least-32-characters-long", "evidentia-api", "evidentia-client", 15*time.Minute)
	realUserID := uuid.New()
	token, _, err := m.CreateAccessToken(realUserID, "LAWYER")
	require.NoError(t, err)

	identity := authpkg.AuthenticatedUser{ID: realUserID, Email: "real-user@example.com", Roles: []string{"LAWYER"}}
	r, seen := newTestRouterWithAuth(m, fakeResolver{identity: identity})

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-User-ID", uuid.New().String())
	req.Header.Set("X-Role", "ADMIN")
	req.Header.Set("X-Admin", "true")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, realUserID, seen.ID, "identity must come from the validated JWT/resolver, never a client header")
	assert.Equal(t, []string{"LAWYER"}, seen.Roles, "role must never be spoofable via X-Role")
}

func TestAuthMiddleware_RejectsTokenWithInvalidSubject(t *testing.T) {
	// A structurally valid, correctly-signed, otherwise-compliant token
	// whose subject is not a parseable UUID must still be rejected by the
	// middleware itself, not passed through with a zero-value ID.
	const signingKey = "test-signing-key-at-least-32-characters-long"
	m := authpkg.NewJWTManager(signingKey, "evidentia-api", "evidentia-client", 15*time.Minute)

	claims := authpkg.Claims{
		Role: "LAWYER",
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    "evidentia-api",
			Subject:   "not-a-uuid",
			Audience:  jwt.ClaimStrings{"evidentia-client"},
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(signingKey))
	require.NoError(t, err)

	r, _ := newTestRouterWithAuth(m, fakeResolver{identity: authpkg.AuthenticatedUser{}})
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}
