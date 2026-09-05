package document

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	authpkg "evidentia/backend/internal/auth"
	"evidentia/backend/internal/service"
	"evidentia/backend/internal/utils"
	"evidentia/backend/pkg/response"
)

// sharesListResponse is GET /documents/:id/shares's response data shape.
type sharesListResponse struct {
	Shares []service.ShareSummary `json:"shares"`
}

// ListShares handles GET /api/v1/documents/:id/shares. Registered behind
// middleware.Auth and
// middleware.RequireDocumentAccess(authz.ActionDocumentShare, "id") — the
// same authority required to CREATE a share also governs who may see its
// share list (master prompt §9); service.ShareService.ListShares
// independently re-checks this.
//
// @Summary      List a document's shares
// @Description  Returns every share ever created for this document, newest first, including revoked/expired ones (historical delegation records are never hidden — see effective_status). Requires document:share plus a relationship to the document's case.
// @Tags         documents
// @Produce      json
// @Security     BearerAuth
// @Param        id   path      string  true  "Document ID (UUID)"
// @Success      200  {object}  response.Envelope{data=sharesListResponse}
// @Failure      401  {object}  response.Envelope  "Authentication required"
// @Failure      403  {object}  response.Envelope  "You do not have permission to perform this action (also returned for a nonexistent document ID)"
// @Router       /api/v1/documents/{id}/shares [get]
func ListShares(svc *service.ShareService) gin.HandlerFunc {
	return func(c *gin.Context) {
		user, ok := authpkg.CurrentUser(c)
		if !ok {
			response.Error(c, http.StatusUnauthorized, utils.CodeUnauthorized, "Authentication required")
			return
		}

		documentID, err := uuid.Parse(c.Param("id"))
		if err != nil {
			response.Error(c, http.StatusForbidden, utils.CodeForbidden, "You do not have permission to perform this action")
			return
		}

		shares, err := svc.ListShares(c.Request.Context(), user, documentID)
		if err != nil {
			writeServiceError(c, err)
			return
		}

		response.Success(c, http.StatusOK, sharesListResponse{Shares: shares})
	}
}
