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

// RequestExport handles POST /api/v1/documents/:id/export
//
// @Summary      Request a forensic evidence export
// @Description  Generates a cryptographically watermarked and hashed export of the document
// @Tags         documents
// @Produce      json
// @Security     BearerAuth
// @Param        id   path  string  true  "Document ID (UUID)"
// @Success      202  {object}  response.Envelope{data=service.EvidenceExportSummary}
// @Failure      401  {object}  response.Envelope  "Authentication required"
// @Failure      403  {object}  response.Envelope  "You do not have permission to perform this action"
// @Failure      500  {object}  response.Envelope
// @Router       /api/v1/documents/{id}/export [post]
func RequestExport(svc *service.ExportService) gin.HandlerFunc {
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

		clientIP := c.ClientIP()
		userAgent := c.GetHeader("User-Agent")

		// Process Export (this immediately generates the export stream and saves it)
		summary, err := svc.RequestExport(c.Request.Context(), user, documentID, clientIP, userAgent)
		if err != nil {
			writeServiceError(c, err)
			return
		}

		response.Success(c, http.StatusAccepted, summary)
	}
}

// DownloadExport handles GET /api/v1/exports/:export_id/download
//
// @Summary      Download an evidence export
// @Description  Streams the watermarked export's bytes by its specific Export ID
// @Tags         exports
// @Produce      application/octet-stream
// @Security     BearerAuth
// @Param        export_id path string true "Export ID (e.g. EXP-...)"
// @Success      200  {file}    binary
// @Failure      401  {object}  response.Envelope
// @Failure      403  {object}  response.Envelope
// @Failure      503  {object}  response.Envelope
// @Router       /api/v1/exports/{export_id}/download [get]
func DownloadExport(svc *service.ExportService) gin.HandlerFunc {
	return func(c *gin.Context) {
		user, ok := authpkg.CurrentUser(c)
		if !ok {
			response.Error(c, http.StatusUnauthorized, utils.CodeUnauthorized, "Authentication required")
			return
		}

		exportID := c.Param("export_id")
		if exportID == "" {
			response.Error(c, http.StatusBadRequest, utils.CodeBadRequest, "Export ID is required")
			return
		}

		result, err := svc.DownloadEvidenceExport(c.Request.Context(), user, exportID)
		if err != nil {
			writeServiceError(c, err)
			return
		}
		defer result.Content.Close()

		disposition := mime.FormatMediaType("attachment", map[string]string{"filename": result.Document.Filename})
		c.Header("Content-Disposition", disposition)
		c.Header("X-Content-Type-Options", "nosniff")

		// Serve the export bytes
		c.DataFromReader(http.StatusOK, -1, result.Document.MimeType, result.Content, nil)
	}
}
