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

// BlockchainProvenance handles GET /api/v1/documents/:id/blockchain/provenance.
// Returns the full ordered history of blockchain anchor records for a document
// (all versions, all event types, including PENDING/CONFIRMED/FAILED rows) so
// that investigators can reconstruct the complete on-chain custody trail.
// Requires document:verify permission — same authorization gate as
// BlockchainStatus and Verify.
//
// @Summary      Get blockchain provenance history
// @Description  Returns all blockchain anchor records for the document in reverse-chronological order. Includes PENDING, CONFIRMED, and FAILED entries. Requires document:verify permission. If the blockchain integration is disabled or the document has no anchors, returns an empty array (not an error).
// @Tags         documents
// @Produce      json
// @Security     BearerAuth
// @Param        id   path      string  true  "Document ID (UUID)"
// @Success      200  {object}  response.Envelope{data=[]service.BlockchainAnchorSummary}
// @Failure      401  {object}  response.Envelope  "Authentication required"
// @Failure      403  {object}  response.Envelope  "Forbidden"
// @Router       /api/v1/documents/{id}/blockchain/provenance [get]
func BlockchainProvenance(svc *service.BlockchainAnchorService) gin.HandlerFunc {
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

		summaries, err := svc.GetAnchorProvenance(c.Request.Context(), user, documentID)
		if err != nil {
			writeServiceError(c, err)
			return
		}

		response.Success(c, http.StatusOK, summaries)
	}
}
