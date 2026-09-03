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

// Update handles PUT /api/v1/admin/users/:id — a full replacement of
// first_name/last_name/display_name/phone (see updateUserRequest's doc
// comment). Registered behind middleware.Auth and
// middleware.RequirePermission(authz.ActionUserUpdate).
//
// @Summary      Update a user's profile (admin)
// @Description  Full replacement of first_name/last_name/display_name/phone. Excludes email/password/role/status, each of which has its own endpoint. Restricted to ADMIN (user:update).
// @Tags         admin
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        id       path      string              true  "User ID (UUID)"
// @Param        request  body      updateUserRequest   true  "Full profile representation"
// @Success      200      {object}  response.Envelope{data=userResponse}
// @Failure      400      {object}  response.Envelope  "Invalid request body"
// @Failure      401      {object}  response.Envelope  "Authentication required"
// @Failure      403      {object}  response.Envelope  "You do not have permission to perform this action"
// @Failure      404      {object}  response.Envelope  "User not found"
// @Router       /api/v1/admin/users/{id} [put]
func Update(svc *service.UserService) gin.HandlerFunc {
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

		var req updateUserRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			response.Error(c, http.StatusBadRequest, utils.CodeBadRequest, "Invalid request body")
			return
		}

		result, err := svc.UpdateUser(c.Request.Context(), actor, id, service.UpdateUserInput{
			FirstName:   req.FirstName,
			LastName:    req.LastName,
			DisplayName: req.DisplayName,
			Phone:       req.Phone,
		})
		if err != nil {
			writeServiceError(c, err)
			return
		}

		response.Success(c, http.StatusOK, result)
	}
}
