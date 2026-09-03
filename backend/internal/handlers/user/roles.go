package user

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"evidentia/backend/internal/service"
	"evidentia/backend/pkg/response"
)

// ListRoles handles GET /api/v1/admin/roles. Registered behind ONLY
// middleware.Auth — it lists the fixed, non-sensitive role catalog, no
// per-user data, so no permission beyond authentication is required (see
// docs/API_ENDPOINTS.md's Admin section and UserService.ListRoles's doc
// comment).
//
// @Summary      List the role catalog
// @Description  Returns the fixed set of roles (ADMIN, POLICE, FORENSICS, LAWYER, JUDGE). Requires authentication only.
// @Tags         admin
// @Produce      json
// @Security     BearerAuth
// @Success      200  {object}  response.Envelope{data=roleListResponse}
// @Failure      401  {object}  response.Envelope  "Authentication required"
// @Router       /api/v1/admin/roles [get]
func ListRoles(svc *service.UserService) gin.HandlerFunc {
	return func(c *gin.Context) {
		result, err := svc.ListRoles(c.Request.Context())
		if err != nil {
			writeServiceError(c, err)
			return
		}
		response.Success(c, http.StatusOK, result)
	}
}
