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

// Verify handles POST /api/v1/documents/:id/verify. Registered behind
// middleware.Auth and
// middleware.RequireDocumentAccess(authz.ActionDocumentVerify, "id") —
// service.DocumentService.VerifyDocument independently re-checks the same
// authorization (see that method's doc comment).
//
// Both a successful match (VERIFIED) and a detected mismatch
// (INTEGRITY_FAILURE) are returned as a normal 200 response — verifying a
// document is a request that SUCCEEDED regardless of what it found;
// finding tampering is a meaningful, correctly-reported result, not a
// request failure. A genuine STORAGE error (the object could not be
// retrieved/hashed at all) is a different thing entirely and surfaces as
// an actual error response (503) — see VerifyDocument's doc comment for
// why the two must never be confused.
//
// @Summary      Verify document integrity
// @Description  Recomputes the document's SHA-256 hash from the object actually stored in MinIO and compares it against the canonical hash recorded in PostgreSQL at upload time. The canonical hash is never modified, regardless of outcome. Requires document:verify plus a relationship to the document's case.
// @Tags         documents
// @Produce      json
// @Security     BearerAuth
// @Param        id   path      string  true  "Document ID (UUID)"
// @Success      200  {object}  response.Envelope{data=service.VerificationResult}  "Always 200 on a completed verification — see status: VERIFIED or INTEGRITY_FAILURE"
// @Failure      401  {object}  response.Envelope  "Authentication required"
// @Failure      403  {object}  response.Envelope  "You do not have permission to perform this action (also returned for a nonexistent document ID)"
// @Failure      503  {object}  response.Envelope  "The document could not be retrieved for verification (storage error, not an integrity finding)"
// @Router       /api/v1/documents/{id}/verify [post]
func Verify(svc *service.DocumentService) gin.HandlerFunc {
	return func(c *gin.Context) {
		user, ok := authpkg.CurrentUser(c)
		if !ok {
			response.Error(c, http.StatusUnauthorized, utils.CodeUnauthorized, "Authentication required")
			return
		}

		documentID, err := uuid.Parse(c.Param("id"))
		if err != nil {
			// Identical to an authorized-but-nonexistent/unrelated document —
			// see middleware.RequireDocumentAccess, which already denies a
			// malformed ID the same way before this handler is reached in
			// the normal request path.
			response.Error(c, http.StatusForbidden, utils.CodeForbidden, "You do not have permission to perform this action")
			return
		}

		result, err := svc.VerifyDocument(c.Request.Context(), user, documentID)
		if err != nil {
			writeServiceError(c, err)
			return
		}

		response.Success(c, http.StatusOK, result)
	}
}
