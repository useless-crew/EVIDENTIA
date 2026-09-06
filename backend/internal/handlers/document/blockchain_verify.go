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

// BlockchainVerify handles POST /api/v1/documents/:id/blockchain/verify.
// Performs a three-way integrity check:
//  1. Recompute SHA-256 from MinIO vs. stored hash in PostgreSQL (detects file tampering).
//  2. Compare stored hash vs. anchored hash on the Fabric ledger (detects DB tampering).
//
// Both a clean VERIFIED result and a HASH_MISMATCH / BLOCKCHAIN_MISMATCH
// finding are returned as HTTP 200 — the verification RPC succeeded regardless
// of what it found. BLOCKCHAIN_UNAVAILABLE is never reported as TAMPERED.
// A genuine storage failure (MinIO unreachable) surfaces as 503.
//
// @Summary      Blockchain three-way integrity verification
// @Description  Performs a three-way integrity check: file hash vs. PostgreSQL hash vs. Hyperledger Fabric ledger hash. Returns a granular status code that precisely identifies which layer disagrees (if any). BLOCKCHAIN_UNAVAILABLE is never collapsed into a tamper finding. Requires document:verify permission.
// @Tags         documents
// @Produce      json
// @Security     BearerAuth
// @Param        id   path      string  true  "Document ID (UUID)"
// @Success      200  {object}  response.Envelope{data=service.BlockchainVerifyResult}  "Always 200 on a completed check — inspect result.status for VERIFIED, HASH_MISMATCH, BLOCKCHAIN_MISMATCH, BLOCKCHAIN_UNAVAILABLE, or BLOCKCHAIN_NOT_ANCHORED"
// @Failure      401  {object}  response.Envelope  "Authentication required"
// @Failure      403  {object}  response.Envelope  "Forbidden"
// @Failure      503  {object}  response.Envelope  "Storage unavailable — document could not be retrieved for hashing"
// @Router       /api/v1/documents/{id}/blockchain/verify [post]
func BlockchainVerify(svc *service.BlockchainAnchorService) gin.HandlerFunc {
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

		result, err := svc.VerifyDocumentBlockchain(c.Request.Context(), user, documentID)
		if err != nil {
			writeServiceError(c, err)
			return
		}

		response.Success(c, http.StatusOK, result)
	}
}
