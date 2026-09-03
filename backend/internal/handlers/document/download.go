package document

import (
	"mime"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	authpkg "evidentia/backend/internal/auth"
	"evidentia/backend/internal/service"
	"evidentia/backend/internal/utils"
	"evidentia/backend/pkg/response"
)

// Download handles GET /api/v1/documents/:id/download. Registered behind
// middleware.Auth and
// middleware.RequireDocumentAccess(authz.ActionDocumentDownload, "id") —
// service.DocumentService.DownloadDocument independently re-checks the
// same authorization, resolves the document under RLS, and only THEN
// retrieves the object from storage (master prompt §54: never touch
// object storage before the database authorization decision succeeds). A
// document that doesn't exist and one the caller has no relationship to
// are indistinguishable in this handler's response (master prompt §29).
//
// The response streams the object directly to the client (gin's
// DataFromReader) — the file is never buffered whole in this process.
//
// @Summary      Download a document
// @Description  Streams the document's raw bytes. Requires document:download plus a relationship to the document's case (inherited entirely from the case — a document carries no independent access grant). Always served as attachment (never rendered inline) to avoid executing untrusted content in a browser.
// @Tags         documents
// @Produce      application/octet-stream
// @Security     BearerAuth
// @Param        id   path  string  true  "Document ID (UUID)"
// @Success      200  {file}    binary
// @Failure      401  {object}  response.Envelope  "Authentication required"
// @Failure      403  {object}  response.Envelope  "You do not have permission to perform this action (also returned for a nonexistent document ID)"
// @Failure      503  {object}  response.Envelope  "The requested document is temporarily unavailable"
// @Router       /api/v1/documents/{id}/download [get]
func Download(svc *service.DocumentService) gin.HandlerFunc {
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

		result, err := svc.DownloadDocument(c.Request.Context(), user, documentID)
		if err != nil {
			writeServiceError(c, err)
			return
		}
		defer result.Content.Close()

		// mime.FormatMediaType RFC 2231-encodes/escapes the filename value
		// (DocumentService already stripped control characters, including
		// CR/LF, at upload time — see sanitizeFilename) — no CRLF/header
		// injection is possible through this value.
		disposition := mime.FormatMediaType("attachment", map[string]string{"filename": result.Document.Filename})
		c.Header("Content-Disposition", disposition)
		// Defense in depth: even though this endpoint always serves
		// "attachment" (never inline), tell the browser not to second-
		// guess the declared Content-Type either (master prompt §25/§32 —
		// never execute/render uploaded content by default).
		c.Header("X-Content-Type-Options", "nosniff")

		c.DataFromReader(http.StatusOK, result.Document.FileSize, result.Document.MimeType, result.Content, nil)
	}
}
