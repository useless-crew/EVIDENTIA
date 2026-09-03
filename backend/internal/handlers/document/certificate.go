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

// Certificate handles GET /api/v1/documents/:id/certificate. Registered
// behind middleware.Auth and
// middleware.RequireDocumentAccess(authz.ActionCertificateRead, "id").
//
// This single endpoint both retrieves an existing certificate and, for a
// caller who also holds certificate:create for this document (ADMIN per
// the seed data), generates one on demand if none exists yet — see
// service.CertificateService.GetOrCreateCertificate's doc comment for the
// full three-way outcome (existing certificate returned / new certificate
// generated / 404 for a reader who cannot generate one). There is
// deliberately no separate POST endpoint: this mirrors the original
// handler stub's own framing ("retrieval/generation trigger") rather than
// inventing an additional route.
//
// @Summary      Get (or generate) a document's compliance certificate
// @Description  Returns the compliance certificate bound to the document's current canonical hash. If none exists and the caller holds certificate:create, generates one: recomputes the document's hash, refuses if it no longer matches the canonical hash (409), and otherwise creates a certificate cryptographically signed over a canonical payload. Requires certificate:read plus a relationship to the document's case.
// @Tags         documents
// @Produce      json
// @Security     BearerAuth
// @Param        id   path      string  true  "Document ID (UUID)"
// @Success      200  {object}  response.Envelope{data=service.CertificateSummary}
// @Failure      401  {object}  response.Envelope  "Authentication required"
// @Failure      403  {object}  response.Envelope  "You do not have permission to perform this action (also returned for a nonexistent document ID)"
// @Failure      404  {object}  response.Envelope  "No compliance certificate exists for this document"
// @Failure      409  {object}  response.Envelope  "Cannot generate a compliance certificate: the document failed integrity verification"
// @Failure      503  {object}  response.Envelope  "The document could not be retrieved to generate a certificate"
// @Router       /api/v1/documents/{id}/certificate [get]
func Certificate(svc *service.CertificateService) gin.HandlerFunc {
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

		result, err := svc.GetOrCreateCertificate(c.Request.Context(), user, documentID)
		if err != nil {
			writeServiceError(c, err)
			return
		}

		response.Success(c, http.StatusOK, result)
	}
}
