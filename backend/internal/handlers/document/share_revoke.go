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

// RevokeShare handles POST /api/v1/documents/:id/shares/:shareId/revoke.
// Registered behind middleware.Auth and
// middleware.RequireDocumentAccess(authz.ActionDocumentShare, "id") —
// service.ShareService.RevokeShare independently re-checks this, and
// scopes the share lookup to BOTH shareId AND the document id in the URL
// (master prompt §16/§50: a shareId belonging to a different document is
// treated identically to a nonexistent one). Revocation immediately
// denies the delegated access (enforced server-side, not merely hidden
// in the UI) and is permanent — there is no un-revoke; a new share must
// be created if access should be granted again (master prompt §32).
//
// @Summary      Revoke a document share
// @Description  Transitions the share from ACTIVE to REVOKED, immediately and permanently denying the delegated access. The share record itself is preserved (never deleted) for audit history. Requires document:share plus a relationship to the document's case.
// @Tags         documents
// @Produce      json
// @Security     BearerAuth
// @Param        id       path      string  true  "Document ID (UUID)"
// @Param        shareId  path      string  true  "Share ID (UUID)"
// @Success      200  {object}  response.Envelope{data=shareResponse}
// @Failure      401  {object}  response.Envelope  "Authentication required"
// @Failure      403  {object}  response.Envelope  "You do not have permission to perform this action (also returned for a nonexistent document ID)"
// @Failure      404  {object}  response.Envelope  "Share not found or already revoked"
// @Router       /api/v1/documents/{id}/shares/{shareId}/revoke [post]
func RevokeShare(svc *service.ShareService) gin.HandlerFunc {
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
		shareID, err := uuid.Parse(c.Param("shareId"))
		if err != nil {
			response.Error(c, http.StatusNotFound, utils.CodeNotFound, "Share not found or already revoked")
			return
		}

		result, err := svc.RevokeShare(c.Request.Context(), user, documentID, shareID)
		if err != nil {
			writeServiceError(c, err)
			return
		}

		response.Success(c, http.StatusOK, result)
	}
}
