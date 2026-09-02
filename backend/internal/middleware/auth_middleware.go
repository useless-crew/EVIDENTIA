package middleware

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	authpkg "evidentia/backend/internal/auth"
	"evidentia/backend/internal/utils"
	"evidentia/backend/pkg/response"
)

// IdentityResolver is the subset of *service.AuthService this middleware
// depends on — declared here (the consumer) rather than importing the
// concrete service type, so tests can substitute a fake and exercise
// header/token handling without a real database. *service.AuthService
// satisfies this interface structurally; no changes to that type are
// needed.
type IdentityResolver interface {
	ResolveIdentity(ctx context.Context, userID uuid.UUID) (authpkg.AuthenticatedUser, error)
}

const bearerPrefix = "Bearer "

// genericUnauthorizedMessage is the ONLY message this middleware ever
// returns to a client — regardless of whether the header was missing,
// malformed, the token expired, had the wrong issuer/audience/algorithm,
// or the account is inactive. Which check failed is logged server-side
// only (master prompt §30/§46).
const genericUnauthorizedMessage = "Authentication required"

// Auth validates the Authorization: Bearer <token> header on every request
// it guards: JWT signature, algorithm, issuer, audience, and expiration
// (via jwtManager.Validate), then re-resolves the caller's CURRENT status
// and roles from the database (via authService.ResolveIdentity) — never
// trusting the JWT's role claim alone, and rejecting a since-deactivated
// user even though their token has not expired (master prompt §15/§62).
// On success, the resolved identity is attached via
// auth.SetAuthenticatedUser; on any failure, it aborts with a single
// generic 401.
func Auth(jwtManager *authpkg.JWTManager, resolver IdentityResolver, logger *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		header := c.GetHeader("Authorization")
		if header == "" {
			deny(c, logger, "missing_header")
			return
		}
		if !strings.HasPrefix(header, bearerPrefix) || len(header) <= len(bearerPrefix) {
			deny(c, logger, "malformed_header")
			return
		}
		tokenString := strings.TrimSpace(strings.TrimPrefix(header, bearerPrefix))
		if tokenString == "" {
			deny(c, logger, "empty_token")
			return
		}

		claims, err := jwtManager.Validate(tokenString)
		if err != nil {
			deny(c, logger, tokenFailureReason(err))
			return
		}

		userID, err := uuid.Parse(claims.Subject)
		if err != nil {
			deny(c, logger, "invalid_subject")
			return
		}

		identity, err := resolver.ResolveIdentity(c.Request.Context(), userID)
		if err != nil {
			deny(c, logger, "identity_unresolvable")
			return
		}

		authpkg.SetAuthenticatedUser(c, identity)
		c.Next()
	}
}

func deny(c *gin.Context, logger *slog.Logger, reason string) {
	logger.WarnContext(c.Request.Context(), "authentication rejected",
		slog.String("request_id", utils.GetRequestID(c)),
		slog.String("reason", reason),
	)
	response.Error(c, http.StatusUnauthorized, utils.CodeUnauthorized, genericUnauthorizedMessage)
	c.Abort()
}

// tokenFailureReason categorizes a JWT validation error for the internal
// diagnostic log only — never surfaced to the client, which always sees
// genericUnauthorizedMessage regardless of which of these matched.
func tokenFailureReason(err error) string {
	switch {
	case errors.Is(err, jwt.ErrTokenExpired):
		return "token_expired"
	case errors.Is(err, jwt.ErrTokenInvalidIssuer):
		return "invalid_issuer"
	case errors.Is(err, jwt.ErrTokenInvalidAudience):
		return "invalid_audience"
	case errors.Is(err, jwt.ErrTokenSignatureInvalid):
		return "invalid_signature"
	case errors.Is(err, jwt.ErrTokenNotValidYet):
		return "not_valid_yet"
	case errors.Is(err, jwt.ErrTokenMalformed):
		return "malformed_token"
	default:
		return "invalid_token"
	}
}
