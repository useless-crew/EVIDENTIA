package cases

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	authpkg "evidentia/backend/internal/auth"
	"evidentia/backend/internal/service"
	"evidentia/backend/internal/utils"
	"evidentia/backend/pkg/response"
)

// Get handles GET /api/v1/cases/:id. Registered behind middleware.Auth and
// middleware.RequireCaseAccess(authz.ActionCaseRead, "id") — that ABAC
// check has already run by the time this handler executes, and
// service.CaseService.GetCase independently re-checks it (see that
// method's doc comment on why). A case that doesn't exist and a case the
// caller has no relationship to are indistinguishable in this handler's
// response (master prompt §14).
//
// @Summary      Get case detail
// @Description  Returns full case detail: metadata, status, involved parties (witness identity redacted per role — see docs/SECURITY.md), document references, and a chronological timeline. Requires case:read plus a relationship to this specific case (creator, active case_members row, or ADMIN).
// @Tags         cases
// @Produce      json
// @Security     BearerAuth
// @Param        id   path      string  true  "Case ID (UUID)"
// @Success      200  {object}  response.Envelope{data=caseDetailResponse}
// @Failure      401  {object}  response.Envelope  "Authentication required"
// @Failure      403  {object}  response.Envelope  "You do not have permission to perform this action (also returned for a nonexistent case ID, to avoid confirming its existence)"
// @Router       /api/v1/cases/{id} [get]
func Get(svc *service.CaseService) gin.HandlerFunc {
	return func(c *gin.Context) {
		user, ok := authpkg.CurrentUser(c)
		if !ok {
			response.Error(c, http.StatusUnauthorized, utils.CodeUnauthorized, "Authentication required")
			return
		}

		caseID, err := uuid.Parse(c.Param("id"))
		if err != nil {
			// Identical to an authorized-but-nonexistent/unrelated case —
			// see service.genericCaseForbiddenMessage's doc comment and
			// middleware.RequireCaseAccess, which already denies a
			// malformed ID the same way before this handler is even
			// reached in the normal request path.
			response.Error(c, http.StatusForbidden, utils.CodeForbidden, "You do not have permission to perform this action")
			return
		}

		result, err := svc.GetCase(c.Request.Context(), user, caseID)
		if err != nil {
			writeServiceError(c, err)
			return
		}

		response.Success(c, http.StatusOK, result)
	}
}
