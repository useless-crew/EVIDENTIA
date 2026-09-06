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

// BlockchainStatus handles GET /api/v1/documents/:id/blockchain/status.
// Returns the most recent blockchain anchor status for the document.
// Requires document:verify permission — same gate as the existing verify
// endpoint, because blockchain provenance is evidence metadata that must
// not be accessible to unauthorized parties.
//
// @Summary      Get blockchain anchor status
// @Description  Returns the most recent Hyperledger Fabric blockchain anchor status for the document. If blockchain is disabled or the document has not been anchored yet, returns null anchor data. Never returns blockchain unavailability as a tamper finding.
// @Tags         documents
// @Produce      json
// @Security     BearerAuth
// @Param        id   path      string  true  "Document ID (UUID)"
// @Success      200  {object}  response.Envelope{data=service.BlockchainAnchorSummary}  "Blockchain anchor status (may be null if no anchor exists)"
// @Failure      401  {object}  response.Envelope  "Authentication required"
// @Failure      403  {object}  response.Envelope  "Forbidden"
// @Failure      503  {object}  response.Envelope  "Service unavailable"
// @Router       /api/v1/documents/{id}/blockchain/status [get]
func BlockchainStatus(svc *service.BlockchainAnchorService) gin.HandlerFunc {
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

		anchor, err := svc.GetAnchorStatus(c.Request.Context(), user, documentID)
		if err != nil {
			writeServiceError(c, err)
			return
		}

		// anchor may be nil if no anchor exists yet — returned as null in JSON.
		response.Success(c, http.StatusOK, anchor)
	}
}
