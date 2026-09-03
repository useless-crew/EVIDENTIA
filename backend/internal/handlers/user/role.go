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

// UpdateRole handles PUT /api/v1/admin/users/:id/role. Registered behind
// ONLY middleware.Auth — authorization here is entirely
// UserService.UpdateRole's call to authz.Service.CanModifyUserRole (RBAC
// user:role PLUS the hard block on an actor modifying their own role), per
// docs/API_ENDPOINTS.md's documented design for this exact route.
//
// @Summary      Change a user's role (admin)
// @Description  Replaces the target user's entire role set with a single new role. Requires user:role AND that the caller is not modifying their own role (self-escalation is always denied, even for an ADMIN acting on their own account).
// @Tags         admin
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        id       path      string              true  "User ID (UUID)"
// @Param        request  body      updateRoleRequest   true  "New role"
// @Success      200      {object}  response.Envelope{data=userResponse}
// @Failure      400      {object}  response.Envelope  "Invalid role"
// @Failure      401      {object}  response.Envelope  "Authentication required"
// @Failure      403      {object}  response.Envelope  "You do not have permission to perform this action (also returned for a self-role-change attempt)"
// @Failure      404      {object}  response.Envelope  "User not found"
// @Router       /api/v1/admin/users/{id}/role [put]
func UpdateRole(svc *service.UserService) gin.HandlerFunc {
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

		var req updateRoleRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			response.Error(c, http.StatusBadRequest, utils.CodeBadRequest, "Invalid request body")
			return
		}

		result, err := svc.UpdateRole(c.Request.Context(), actor, id, req.Role)
		if err != nil {
			writeServiceError(c, err)
			return
		}

		response.Success(c, http.StatusOK, result)
	}
}
