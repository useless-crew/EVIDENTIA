package user

import (
	"net/http"

	"github.com/gin-gonic/gin"

	authpkg "evidentia/backend/internal/auth"
	"evidentia/backend/internal/service"
	"evidentia/backend/internal/utils"
	"evidentia/backend/pkg/response"
)

// searchResponse is GET /users/search's response data shape.
type searchResponse struct {
	Users []service.RecipientCandidate `json:"users"`
}

// Search handles GET /api/v1/users/search?q=.... Registered behind
// middleware.Auth only — deliberately NOT middleware.RequirePermission
// (authz.ActionUserRead, ADMIN-only global user management): this is a
// narrower, safer capability any authenticated user needs to find a
// share recipient (master prompt §38), returning only a small, safe
// field subset for active users matching a REQUIRED search query, capped
// well below a normal page size — see
// service.ShareService.SearchRecipients's doc comment for the full
// anti-enumeration rationale (master prompt §48).
//
// @Summary      Search for a potential share recipient
// @Description  Returns up to 10 ACTIVE users whose name/email matches q (case-insensitive substring) — the document-sharing recipient picker's data source. Requires authentication only; q must be at least 2 characters.
// @Tags         users
// @Produce      json
// @Security     BearerAuth
// @Param        q    query     string  true  "Search text (name or email substring), minimum 2 characters"
// @Success      200  {object}  response.Envelope{data=searchResponse}
// @Failure      400  {object}  response.Envelope  "Search query too short/long"
// @Failure      401  {object}  response.Envelope  "Authentication required"
// @Router       /api/v1/users/search [get]
func Search(svc *service.ShareService) gin.HandlerFunc {
	return func(c *gin.Context) {
		user, ok := authpkg.CurrentUser(c)
		if !ok {
			response.Error(c, http.StatusUnauthorized, utils.CodeUnauthorized, "Authentication required")
			return
		}

		candidates, err := svc.SearchRecipients(c.Request.Context(), user, c.Query("q"))
		if err != nil {
			writeServiceError(c, err)
			return
		}

		response.Success(c, http.StatusOK, searchResponse{Users: candidates})
	}
}
