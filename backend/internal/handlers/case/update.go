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

// Update handles PUT /api/v1/cases/:id — a full replacement of title/
// description/status/metadata (see updateCaseRequest's doc comment).
// Registered behind middleware.Auth and
// middleware.RequireCaseAccess(authz.ActionCaseUpdate, "id");
// service.CaseService.UpdateCase independently re-checks the same
// authorization and additionally validates the requested status
// transition (master prompt §16/§18).
//
// @Summary      Update a case
// @Description  Full replacement of title/description/status/metadata. id/created_by/created_at are never accepted from the client. Requires case:update plus a relationship to this specific case. Invalid status transitions are rejected.
// @Tags         cases
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        id       path      string             true  "Case ID (UUID)"
// @Param        request  body      updateCaseRequest  true  "Full case representation"
// @Success      200      {object}  response.Envelope{data=caseDetailResponse}
// @Failure      400      {object}  response.Envelope  "Invalid request body, invalid status, or invalid status transition"
// @Failure      401      {object}  response.Envelope  "Authentication required"
// @Failure      403      {object}  response.Envelope  "You do not have permission to perform this action (also returned for a nonexistent case ID)"
// @Router       /api/v1/cases/{id} [put]
func Update(svc *service.CaseService) gin.HandlerFunc {
	return func(c *gin.Context) {
		user, ok := authpkg.CurrentUser(c)
		if !ok {
			response.Error(c, http.StatusUnauthorized, utils.CodeUnauthorized, "Authentication required")
			return
		}

		caseID, err := uuid.Parse(c.Param("id"))
		if err != nil {
			response.Error(c, http.StatusForbidden, utils.CodeForbidden, "You do not have permission to perform this action")
			return
		}

		var req updateCaseRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			response.Error(c, http.StatusBadRequest, utils.CodeBadRequest, "Invalid request body")
			return
		}

		result, err := svc.UpdateCase(c.Request.Context(), user, caseID, service.UpdateCaseInput{
			Title:       req.Title,
			Description: req.Description,
			Status:      req.Status,
			Metadata:    req.Metadata,
		})
		if err != nil {
			writeServiceError(c, err)
			return
		}

		response.Success(c, http.StatusOK, result)
	}
}
