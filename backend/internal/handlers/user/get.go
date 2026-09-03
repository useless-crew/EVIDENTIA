package user

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	authpkg "evidentia/backend/internal/auth"
	"evidentia/backend/internal/service"
	"evidentia/backend/internal/utils"
	"evidentia/backend/pkg/response"
)

// Get handles GET /api/v1/admin/users/:id. Registered behind
// middleware.Auth and middleware.RequirePermission(authz.ActionUserRead).
//
// @Summary      Get user detail (admin)
// @Description  Returns a single user's profile, status, and role set. Restricted to ADMIN (user:read).
// @Tags         admin
// @Produce      json
// @Security     BearerAuth
// @Param        id   path      string  true  "User ID (UUID)"
// @Success      200  {object}  response.Envelope{data=userResponse}
// @Failure      401  {object}  response.Envelope  "Authentication required"
// @Failure      403  {object}  response.Envelope  "You do not have permission to perform this action"
// @Failure      404  {object}  response.Envelope  "User not found"
// @Router       /api/v1/admin/users/{id} [get]
func Get(svc *service.UserService) gin.HandlerFunc {
	return func(c *gin.Context) {
		actor, ok := authpkg.CurrentUser(c)
		if !ok {
			response.Error(c, http.StatusUnauthorized, utils.CodeUnauthorized, "Authentication required")
			return
		}

		id, err := uuid.Parse(c.Param("id"))
		if err != nil {
			response.Error(c, http.StatusNotFound, utils.CodeNotFound, "User not found")
			return
		}

		result, err := svc.GetUser(c.Request.Context(), actor, id)
		if err != nil {
			writeServiceError(c, err)
			return
		}

		response.Success(c, http.StatusOK, result)
	}
}
