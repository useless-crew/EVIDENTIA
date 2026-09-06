package document

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	authpkg "evidentia/backend/internal/auth"
	"evidentia/backend/internal/service"
	"evidentia/backend/internal/utils"
	"evidentia/backend/pkg/response"
)

// shareRequest is POST /documents/:id/share's request body binding
// target. ExpiresAt is a pointer so "field omitted" (non-expiring) is
// distinguishable from any zero value — validated by
// service.ShareService.CreateShare, never here.
type shareRequest struct {
	UserID     string     `json:"user_id"`
	Permission string     `json:"permission"`
	ExpiresAt  *time.Time `json:"expires_at"`
	Reason     *string    `json:"reason"`
}

// shareResponse aliases the service's own response DTO — same pattern as
// documentResponse above.
type shareResponse = service.ShareSummary

// Share handles POST /api/v1/documents/:id/share. Registered behind
// middleware.Auth and
// middleware.RequireDocumentAccess(authz.ActionDocumentShare, "id") —
// service.ShareService.CreateShare independently re-checks the same
// authorization (see that method's doc comment), validates the request,
// and verifies the recipient is a real, active, distinct Evidentia user
// before persisting a new share. The recipient does NOT become the
// document's owner — this only grants a specific, revocable permission
// (VIEW or VERIFY) on this exact document.
//
// @Summary      Share a document with another user
// @Description  Grants shared_with_user_id a specific, revocable, optionally time-bounded permission (VIEW or VERIFY) on this exact document — never ownership, never resharing, never redaction. Requires document:share plus a relationship to the document's case.
// @Tags         documents
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        id    path  string         true  "Document ID (UUID)"
// @Param        body  body  shareRequest   true  "Recipient, permission, and optional expiration/reason"
// @Success      201  {object}  response.Envelope{data=shareResponse}
// @Failure      400  {object}  response.Envelope  "Invalid request, permission, expiration, or recipient"
// @Failure      401  {object}  response.Envelope  "Authentication required"
// @Failure      403  {object}  response.Envelope  "You do not have permission to perform this action (also returned for a nonexistent document ID)"
// @Failure      409  {object}  response.Envelope  "An active share already exists for this recipient on this document"
// @Router       /api/v1/documents/{id}/share [post]
func Share(svc *service.ShareService) gin.HandlerFunc {
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

		var req shareRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			response.Error(c, http.StatusBadRequest, utils.CodeBadRequest, "Invalid request body")
			return
		}

		recipientID, err := uuid.Parse(req.UserID)
		if err != nil {
			response.Error(c, http.StatusBadRequest, utils.CodeBadRequest, "user_id must be a valid UUID")
			return
		}

		result, err := svc.CreateShare(c.Request.Context(), user, documentID, service.CreateShareInput{
			RecipientUserID: recipientID,
			Permission:      req.Permission,
			ExpiresAt:       req.ExpiresAt,
			Reason:          req.Reason,
		})
		if err != nil {
			writeServiceError(c, err)
			return
		}

		response.Success(c, http.StatusCreated, result)
	}
}
