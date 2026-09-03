package cases

import (
	"net/http"

	"github.com/gin-gonic/gin"

	authpkg "evidentia/backend/internal/auth"
	"evidentia/backend/internal/service"
	"evidentia/backend/internal/utils"
	"evidentia/backend/pkg/response"
)

// Create handles POST /api/v1/cases. Registered behind middleware.Auth and
// middleware.RequirePermission(authz.ActionCaseCreate) — see
// internal/httpserver/router.go — so by the time this handler runs the
// caller is authenticated and RBAC-authorized to create a case; there is
// no resource yet for an ABAC check to run against (case creation has no
// existing case to be related to).
//
// @Summary      Create a case
// @Description  Creates a new case. Restricted to POLICE and ADMIN (case:create). created_by is always the authenticated caller — never a client-supplied value.
// @Tags         cases
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        request  body      createCaseRequest  true  "Case to create"
// @Success      201      {object}  response.Envelope{data=caseDetailResponse}
// @Failure      400      {object}  response.Envelope  "Invalid request body"
// @Failure      401      {object}  response.Envelope  "Authentication required"
// @Failure      403      {object}  response.Envelope  "You do not have permission to perform this action"
// @Failure      409      {object}  response.Envelope  "A case with this case number already exists"
// @Router       /api/v1/cases [post]
func Create(svc *service.CaseService) gin.HandlerFunc {
	return func(c *gin.Context) {
		user, ok := authpkg.CurrentUser(c)
		if !ok {
			response.Error(c, http.StatusUnauthorized, utils.CodeUnauthorized, "Authentication required")
			return
		}

		var req createCaseRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			response.Error(c, http.StatusBadRequest, utils.CodeBadRequest, "Invalid request body")
			return
		}

		result, err := svc.CreateCase(c.Request.Context(), user, service.CreateCaseInput{
			CaseNumber:  req.CaseNumber,
			Title:       req.Title,
			Description: req.Description,
			Status:      req.Status,
			Metadata:    req.Metadata,
		})
		if err != nil {
			writeServiceError(c, err)
			return
		}

		response.Success(c, http.StatusCreated, result)
	}
}
