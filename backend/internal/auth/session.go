package auth

import (
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// AuthenticatedUser is the identity internal/middleware/auth_middleware.go
// attaches to a request's context once a JWT has been fully validated AND
// the user's current status/roles have been freshly resolved from the
// database (never trusted from the JWT alone — see Claims.Role and master
// prompt §15). Roles is deliberately plural: System 2's schema is
// many-to-many (user_roles), so a user is not assumed to have exactly one
// role.
type AuthenticatedUser struct {
	ID    uuid.UUID
	Email string
	Roles []string
}

// authenticatedUserKey is the sole gin.Context key this package uses for
// identity — never an arbitrary string scattered across handlers, and
// never a client-supplied header (a client-sent X-User-ID/X-Role must
// never be trusted; see master prompt §63).
const authenticatedUserKey = "auth.authenticated_user"

// SetAuthenticatedUser stores u on c for downstream middleware/handlers.
// Called only by internal/middleware/auth_middleware.go, after full JWT
// and account-status verification.
func SetAuthenticatedUser(c *gin.Context, u AuthenticatedUser) {
	c.Set(authenticatedUserKey, u)
}

// CurrentUser returns the authenticated user attached to c, or ok=false if
// none is present (e.g. an unprotected route, or a handler unit test that
// didn't run the auth middleware). Callers must handle ok==false as an
// authentication error — never panic, never assume authentication
// occurred just because a handler is reached.
func CurrentUser(c *gin.Context) (AuthenticatedUser, bool) {
	v, ok := c.Get(authenticatedUserKey)
	if !ok {
		return AuthenticatedUser{}, false
	}
	u, ok := v.(AuthenticatedUser)
	return u, ok
}
