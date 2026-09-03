package user

import (
	"net/http"

	"github.com/gin-gonic/gin"

	authpkg "evidentia/backend/internal/auth"
	"evidentia/backend/internal/service"
	"evidentia/backend/internal/utils"
	"evidentia/backend/pkg/response"
)

// Create handles POST /api/v1/admin/users. Registered behind middleware.Auth
// and middleware.RequirePermission(authz.ActionUserCreate) — see
// internal/httpserver/router.go — so by the time this handler runs the
// caller is authenticated and RBAC-authorized to create a user;
// UserService.CreateUser independently re-checks the same permission.
//
// @Summary      Create a user (admin)
// @Description  Creates a new user account with an initial password and a single role. Restricted to ADMIN (user:create). id/created_at/updated_at are always server-controlled — never client-supplied.
// @Tags         admin
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        request  body      createUserRequest  true  "User to create"
// @Success      201      {object}  response.Envelope{data=userResponse}
// @Failure      400      {object}  response.Envelope  "Invalid request body, role, or status"
// @Failure      401      {object}  response.Envelope  "Authentication required"
// @Failure      403      {object}  response.Envelope  "You do not have permission to perform this action"
// @Failure      409      {object}  response.Envelope  "A user with this email already exists"
// @Router       /api/v1/admin/users [post]
func Create(svc *service.UserService) gin.HandlerFunc {
	return func(c *gin.Context) {
		user, ok := authpkg.CurrentUser(c)
		if !ok {
			response.Error(c, http.StatusUnauthorized, utils.CodeUnauthorized, "Authentication required")
			return
		}

		var req createUserRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			response.Error(c, http.StatusBadRequest, utils.CodeBadRequest, "Invalid request body")
			return
		}

		result, err := svc.CreateUser(c.Request.Context(), user, service.CreateUserInput{
			Email:       req.Email,
			Password:    req.Password,
			FirstName:   req.FirstName,
			LastName:    req.LastName,
			DisplayName: req.DisplayName,
			Phone:       req.Phone,
			Role:        req.Role,
			Status:      req.Status,
		})
		if err != nil {
			writeServiceError(c, err)
			return
		}

		response.Success(c, http.StatusCreated, result)
	}
}
