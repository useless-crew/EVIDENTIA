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

// UpdateStatus handles PUT /api/v1/admin/users/:id/status. Registered
// behind middleware.Auth and
// middleware.RequirePermission(authz.ActionUserDeactivate);
// UserService.UpdateStatus independently re-checks the same permission and
// additionally blocks an actor from changing their own status.
//
// @Summary      Change a user's account status (admin)
// @Description  Sets a user's status to active, inactive, or suspended. A non-active status immediately revokes every one of that user's refresh sessions, so an already-issued token cannot keep a session alive. An actor cannot change their own status. Restricted to ADMIN (user:deactivate).
// @Tags         admin
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        id       path      string                 true  "User ID (UUID)"
// @Param        request  body      updateStatusRequest    true  "New status"
// @Success      200      {object}  response.Envelope{data=userResponse}
// @Failure      400      {object}  response.Envelope  "Invalid status"
// @Failure      401      {object}  response.Envelope  "Authentication required"
// @Failure      403      {object}  response.Envelope  "You do not have permission to perform this action (also returned for a self-status-change attempt)"
// @Failure      404      {object}  response.Envelope  "User not found"
// @Router       /api/v1/admin/users/{id}/status [put]
func UpdateStatus(svc *service.UserService) gin.HandlerFunc {
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

		var req updateStatusRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			response.Error(c, http.StatusBadRequest, utils.CodeBadRequest, "Invalid request body")
			return
		}

		result, err := svc.UpdateStatus(c.Request.Context(), actor, id, req.Status)
		if err != nil {
			writeServiceError(c, err)
			return
		}

		response.Success(c, http.StatusOK, result)
	}
}
