package document

import (
	"errors"
	"io"
	"mime/multipart"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	authpkg "evidentia/backend/internal/auth"
	"evidentia/backend/internal/service"
	"evidentia/backend/internal/utils"
	"evidentia/backend/pkg/response"
)

// maxDocumentTypeFieldBytes/maxDescriptionFieldBytes bound how many bytes
// this handler will read for the small multipart TEXT fields before
// giving up — independent of DocumentsConfig.MaxUploadSize, which governs
// only the `file` part. These are deliberately small: document_type is a
// short enum value, description mirrors CaseService's own description
// limit for consistency.
const (
	maxDocumentTypeFieldBytes = 64
	maxDescriptionFieldBytes  = 10_000
)

var errFieldTooLarge = errors.New("document: multipart text field exceeds maximum size")

// writeMultipartReadError renders a failure from c.Request.MultipartReader
// or mr.NextPart(). The whole-request body-size guard
// (middleware.BodyLimit(a.Config.Documents.MaxUploadSize) — see
// internal/httpserver/router.go) and DocumentService's own fine-grained,
// file-part-only size guard are two independent, redundant defenses
// (master prompt §12/§49): both ultimately mean "you sent too much data,"
// so both must produce the SAME 413 response — a client should never see
// a different status code depending on which internal layer happened to
// catch it first.
func writeMultipartReadError(c *gin.Context, err error) {
	var maxBytesErr *http.MaxBytesError
	if errors.As(err, &maxBytesErr) {
		response.Error(c, http.StatusRequestEntityTooLarge, utils.CodeRequestEntityTooLarge, "Request body exceeds the maximum upload size")
		return
	}
	response.Error(c, http.StatusBadRequest, utils.CodeBadRequest, "Invalid multipart/form-data request")
}

// Upload handles POST /api/v1/cases/:id/documents — multipart/form-data
// with fields `document_type`, `description` (optional), and `file`.
// Registered behind middleware.Auth and
// middleware.RequireCaseAccess(authz.ActionDocumentUpload, "id") — see
// internal/httpserver/router.go — so by the time this handler runs the
// caller already holds document:upload AND a relationship to this case;
// service.DocumentService.UploadDocument independently re-checks the same
// authorization (see that method's doc comment).
//
// The body is read as a TRUE STREAM via http.Request.MultipartReader —
// never c.Request.ParseMultipartForm/c.FormFile, both of which buffer the
// whole part to memory or a temp file before a handler ever sees it
// (master prompt §13: memory usage must stay ~independent of file size).
// Because a stream can only be consumed once, in the order the client
// sent it, `document_type` (and `description`, if present) MUST appear
// BEFORE `file` in the multipart body — this is a documented API
// contract (see docs/API_ENDPOINTS.md), not an accident: it matches how a
// browser's FormData/JS multipart encoder naturally serializes fields in
// append order. The handler stops reading the request body as soon as
// `file` has been fully processed; any bytes after that are left
// undrained (Go's HTTP server handles this — the connection may not be
// reused for keep-alive, which is an acceptable, standard trade-off, not
// a correctness issue).
//
// @Summary      Upload a document to a case
// @Description  Streams the file to object storage while computing its SHA-256 hash, then persists metadata. Restricted to POLICE/FORENSICS/ADMIN (document:upload) plus a relationship to this specific case. document_type and description fields must precede file in the multipart body.
// @Tags         documents
// @Accept       multipart/form-data
// @Produce      json
// @Security     BearerAuth
// @Param        id             path      string  true   "Case ID (UUID)"
// @Param        document_type  formData  string  true   "FIR | FORENSIC_REPORT | PHOTO_EVIDENCE | WITNESS_STATEMENT | OTHER"
// @Param        description    formData  string  false  "Optional description"
// @Param        file           formData  file    true   "The evidence file"
// @Success      201  {object}  response.Envelope{data=documentResponse}
// @Failure      400  {object}  response.Envelope  "Invalid request, missing file, or invalid document_type"
// @Failure      401  {object}  response.Envelope  "Authentication required"
// @Failure      403  {object}  response.Envelope  "You do not have permission to perform this action"
// @Failure      413  {object}  response.Envelope  "File exceeds the maximum upload size"
// @Router       /api/v1/cases/{id}/documents [post]
func Upload(svc *service.DocumentService) gin.HandlerFunc {
	return func(c *gin.Context) {
		user, ok := authpkg.CurrentUser(c)
		if !ok {
			response.Error(c, http.StatusUnauthorized, utils.CodeUnauthorized, "Authentication required")
			return
		}

		caseID, err := uuid.Parse(c.Param("id"))
		if err != nil {
			// Identical to an authorized-but-nonexistent/unrelated case —
			// see middleware.RequireCaseAccess, which already denies a
			// malformed ID the same way before this handler is reached in
			// the normal request path.
			response.Error(c, http.StatusForbidden, utils.CodeForbidden, "You do not have permission to perform this action")
			return
		}

		mr, err := c.Request.MultipartReader()
		if err != nil {
			writeMultipartReadError(c, err)
			return
		}

		var documentType string
		var haveDocumentType bool
		var description *string

		for {
			part, err := mr.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				writeMultipartReadError(c, err)
				return
			}

			switch part.FormName() {
			case "document_type":
				v, err := readFormValue(part, maxDocumentTypeFieldBytes)
				_ = part.Close()
				if err != nil {
					response.Error(c, http.StatusBadRequest, utils.CodeBadRequest, "document_type is too large")
					return
				}
				documentType = v
				haveDocumentType = true

			case "description":
				v, err := readFormValue(part, maxDescriptionFieldBytes)
				_ = part.Close()
				if err != nil {
					response.Error(c, http.StatusBadRequest, utils.CodeBadRequest, "description is too large")
					return
				}
				description = &v

			case "file":
				if !haveDocumentType {
					_ = part.Close()
					response.Error(c, http.StatusBadRequest, utils.CodeBadRequest, "document_type is required and must precede file in the multipart body")
					return
				}

				result, err := svc.UploadDocument(c.Request.Context(), user, caseID, service.UploadDocumentInput{
					DocumentType: documentType,
					Description:  description,
					Filename:     part.FileName(),
					File:         part,
				})
				_ = part.Close()
				if err != nil {
					writeServiceError(c, err)
					return
				}

				response.Success(c, http.StatusCreated, result)
				return

			default:
				_ = part.Close()
			}
		}

		response.Error(c, http.StatusBadRequest, utils.CodeBadRequest, "file is required")
	}
}

// readFormValue reads a small multipart text field fully, rejecting
// (rather than silently truncating) anything over maxBytes.
func readFormValue(part *multipart.Part, maxBytes int64) (string, error) {
	data, err := io.ReadAll(io.LimitReader(part, maxBytes+1))
	if err != nil {
		return "", err
	}
	if int64(len(data)) > maxBytes {
		return "", errFieldTooLarge
	}
	return string(data), nil
}
