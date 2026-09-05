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

// redactRequest is POST /documents/:id/redact's request body binding
// target. Regions reuses service.RedactRegion directly — its JSON tags
// ARE the wire contract, never a duplicate handler-side type re-mapped
// into it (see RedactRegion's own doc comment). Every field is validated
// by service.DocumentService.RedactDocument, never here — this handler
// only parses/shapes, per this package's own doc comment.
type redactRequest struct {
	Reason  string                 `json:"reason"`
	Regions []service.RedactRegion `json:"regions"`
}

// redactionResponse aliases the service's own response DTO — same pattern
// as documentResponse above.
type redactionResponse = service.RedactionSummary

// Redact handles POST /api/v1/documents/:id/redact. Registered behind
// middleware.Auth, middleware.RequireDocumentAccess(authz.ActionDocumentRedact, "id"),
// and the shared JSON body-size limit (see internal/httpserver/router.go).
// service.DocumentService.RedactDocument independently re-checks the same
// authorization (see that method's doc comment), re-verifies the source
// document's integrity, and — for supported image formats only —
// genuinely removes the requested regions' pixel content before storing
// the result as a brand-new, independent document. The source document is
// never read-modify-written by this call.
//
// @Summary      Create a redacted derivative of a document
// @Description  Produces a NEW document (plus a linked redaction record) with the requested rectangular regions permanently removed from the image's actual pixel data — never a visual overlay. The source document's row, object, and SHA-256 hash are never modified. Supported formats: image/png, image/jpeg only (422 for anything else, including PDFs — this project has no verified safe redaction path for them yet). Requires document:redact plus a relationship to the document's case.
// @Tags         documents
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        id    path  string         true  "Source document ID (UUID)"
// @Param        body  body  redactRequest  true  "Redaction reason and regions"
// @Success      201  {object}  response.Envelope{data=redactionResponse}
// @Failure      400  {object}  response.Envelope  "Invalid request, reason, or region data"
// @Failure      401  {object}  response.Envelope  "Authentication required"
// @Failure      403  {object}  response.Envelope  "You do not have permission to perform this action (also returned for a nonexistent document ID)"
// @Failure      409  {object}  response.Envelope  "The source document failed integrity verification"
// @Failure      422  {object}  response.Envelope  "Redaction is not supported for this document's file type"
// @Failure      503  {object}  response.Envelope  "The document could not be retrieved for redaction"
// @Router       /api/v1/documents/{id}/redact [post]
func Redact(svc *service.DocumentService) gin.HandlerFunc {
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

		var req redactRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			response.Error(c, http.StatusBadRequest, utils.CodeBadRequest, "Invalid request body")
			return
		}

		result, err := svc.RedactDocument(c.Request.Context(), user, documentID, service.RedactDocumentInput{
			Reason:  req.Reason,
			Regions: req.Regions,
		})
		if err != nil {
			writeServiceError(c, err)
			return
		}

		response.Success(c, http.StatusCreated, result)
	}
}
