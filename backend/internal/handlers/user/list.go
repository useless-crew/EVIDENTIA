package user

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	authpkg "evidentia/backend/internal/auth"
	"evidentia/backend/internal/service"
	"evidentia/backend/internal/utils"
	"evidentia/backend/pkg/response"
)

// List handles GET /api/v1/admin/users. Registered behind middleware.Auth
// and middleware.RequirePermission(authz.ActionUserRead) — restricted to
// ADMIN per the seed data (there is no per-caller RLS scoping for this
// listing, unlike cases: it is a global administrative view, not a
// personal one).
//
// @Summary      List users (admin)
// @Description  Returns the filtered, paginated user catalog. Restricted to ADMIN (user:read).
// @Tags         admin
// @Produce      json
// @Security     BearerAuth
// @Param        role       query     string  false  "Filter by exact role (ADMIN, POLICE, FORENSICS, LAWYER, JUDGE)"
// @Param        status     query     string  false  "Filter by exact status (active, inactive, suspended)"
// @Param        search     query     string  false  "Filter by substring match on email/first_name/last_name/display_name"
// @Param        page       query     int     false  "Page number, default 1"
// @Param        page_size  query     int     false  "Rows per page, default 20, max 100"
// @Success      200  {object}  response.Envelope{data=userListResponse}
// @Failure      400  {object}  response.Envelope  "Invalid filter value"
// @Failure      401  {object}  response.Envelope  "Authentication required"
// @Failure      403  {object}  response.Envelope  "You do not have permission to perform this action"
// @Router       /api/v1/admin/users [get]
func List(svc *service.UserService) gin.HandlerFunc {
	return func(c *gin.Context) {
		actor, ok := authpkg.CurrentUser(c)
		if !ok {
			response.Error(c, http.StatusUnauthorized, utils.CodeUnauthorized, "Authentication required")
			return
		}

		var filter service.UserListFilter
		if v := c.Query("role"); v != "" {
			filter.Role = &v
		}
		if v := c.Query("status"); v != "" {
			filter.Status = &v
		}
		if v := c.Query("search"); v != "" {
			filter.Search = &v
		}

		page := utils.ParsePagination(parseInt32(c.Query("page")), parseInt32(c.Query("page_size")))

		result, err := svc.ListUsers(c.Request.Context(), actor, filter, page)
		if err != nil {
			writeServiceError(c, err)
			return
		}

		response.Success(c, http.StatusOK, result)
	}
}

// parseInt32 returns 0 (== "not supplied", see utils.ParsePagination) for
// an empty or malformed value rather than erroring — matches
// handlers/case/list.go's identical helper.
func parseInt32(v string) int32 {
	if v == "" {
		return 0
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return 0
	}
	return int32(n)
}
