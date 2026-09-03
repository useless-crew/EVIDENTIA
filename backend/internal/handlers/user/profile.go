package user

import (
	"net/http"

	"github.com/gin-gonic/gin"

	authpkg "evidentia/backend/internal/auth"
	"evidentia/backend/internal/service"
	"evidentia/backend/internal/utils"
	"evidentia/backend/pkg/response"
)

// Profile handles GET /api/v1/users/me. Registered behind ONLY
// middleware.Auth (no RBAC beyond authentication — every authenticated
// user may view their own profile, regardless of role). Deliberately does
// NOT call UserService.GetUser — that method requires user:read (an
// admin-only permission per the seed data), and a non-admin caller must
// still be able to see their own record — so this calls the lower-level,
// no-authorization-check UserService.GetOwnProfile instead.
//
// @Summary      Get the current user's own profile
// @Description  Returns the authenticated caller's own profile, status, and role set. Requires authentication only — no RBAC permission (every role may view their own profile).
// @Tags         users
// @Produce      json
// @Security     BearerAuth
// @Success      200  {object}  response.Envelope{data=userResponse}
// @Failure      401  {object}  response.Envelope  "Authentication required"
// @Router       /api/v1/users/me [get]
func Profile(svc *service.UserService) gin.HandlerFunc {
	return func(c *gin.Context) {
		actor, ok := authpkg.CurrentUser(c)
		if !ok {
			response.Error(c, http.StatusUnauthorized, utils.CodeUnauthorized, "Authentication required")
			return
		}

		result, err := svc.GetOwnProfile(c.Request.Context(), actor)
		if err != nil {
			writeServiceError(c, err)
			return
		}

		response.Success(c, http.StatusOK, result)
	}
}
