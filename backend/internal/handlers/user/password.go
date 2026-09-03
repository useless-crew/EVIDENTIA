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

// ResetPassword handles PUT /api/v1/admin/users/:id/password. Registered
// behind middleware.Auth and
// middleware.RequirePermission(authz.ActionUserUpdate). This is the
// project's admin-driven password-reset mechanism (master prompt §13: no
// email/token flow is implemented, since none is otherwise in scope) —
// every existing session for the target user is revoked as part of the
// reset.
//
// @Summary      Reset a user's password (admin)
// @Description  Sets a new password for the target user and revokes every one of their existing refresh sessions. The new password is never returned or logged. Restricted to ADMIN (user:update).
// @Tags         admin
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        id       path      string                 true  "User ID (UUID)"
// @Param        request  body      resetPasswordRequest   true  "New password"
// @Success      204      "No content"
// @Failure      400      {object}  response.Envelope  "Password too short"
// @Failure      401      {object}  response.Envelope  "Authentication required"
// @Failure      403      {object}  response.Envelope  "You do not have permission to perform this action"
// @Failure      404      {object}  response.Envelope  "User not found"
// @Router       /api/v1/admin/users/{id}/password [put]
func ResetPassword(svc *service.UserService) gin.HandlerFunc {
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

		var req resetPasswordRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			response.Error(c, http.StatusBadRequest, utils.CodeBadRequest, "Invalid request body")
			return
		}

		if err := svc.ResetPassword(c.Request.Context(), actor, id, req.Password); err != nil {
			writeServiceError(c, err)
			return
		}

		c.Status(http.StatusNoContent)
	}
}
