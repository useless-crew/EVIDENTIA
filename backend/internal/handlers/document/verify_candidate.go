package document

import (
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	authpkg "evidentia/backend/internal/auth"
	"evidentia/backend/internal/service"
	"evidentia/backend/internal/utils"
	"evidentia/backend/pkg/response"
)

// VerifyCandidate handles POST /api/v1/documents/:id/verify-candidate.
// Registered behind middleware.Auth and
// middleware.RequireDocumentAccess(authz.ActionDocumentVerify, "id").
//
// Supports two verification sources:
//  1. "stored": Re-verifies the actual evidence file currently residing in MinIO
//     against the canonical PostgreSQL hash and Hyperledger Fabric blockchain anchor.
//  2. "candidate": Streams an uploaded local candidate file directly to compute its
//     SHA-256 hash without ever persisting it to MinIO, PostgreSQL, or disk.
//
// Both clean matches (VERIFIED) and detected tamper/integrity failures (INTEGRITY_FAILURE)
// are returned as HTTP 200 — verifying evidence is an operation that succeeded regardless
// of what it found; finding tampering is a valid, correctly-reported cryptographic result.
// Genuine infrastructure failures (storage/database errors) return appropriate 5xx errors.
//
// @Summary      Verify evidence integrity against candidate or stored file
// @Description  Streams a verification candidate file (or checks stored evidence) to compute SHA-256 and compares it against canonical PostgreSQL metadata and the Hyperledger Fabric blockchain anchor. The trusted reference is never mutated.
// @Tags         documents
// @Accept       multipart/form-data
// @Produce      json
// @Security     BearerAuth
// @Param        id      path      string  true   "Document ID (UUID)"
// @Param        source  query     string  false  "'stored' to verify MinIO object, or 'candidate' (default)"
// @Param        file    formData  file    false  "Candidate evidence file to verify (for source=candidate)"
// @Success      200     {object}  response.Envelope{data=service.BlockchainVerifyResult}
// @Failure      400     {object}  response.Envelope  "Invalid request or missing candidate file"
// @Failure      401     {object}  response.Envelope  "Authentication required"
// @Failure      403     {object}  response.Envelope  "Forbidden"
// @Failure      413     {object}  response.Envelope  "Candidate file exceeds maximum allowable size"
// @Failure      503     {object}  response.Envelope  "Storage unavailable"
// @Router       /api/v1/documents/{id}/verify-candidate [post]
func VerifyCandidate(svc *service.BlockchainAnchorService) gin.HandlerFunc {
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

		sourceParam := strings.TrimSpace(strings.ToLower(c.Query("source")))
		if sourceParam == "stored" {
			result, err := svc.VerifyCandidate(c.Request.Context(), user, documentID, nil, 0, "", true)
			if err != nil {
				writeServiceError(c, err)
				return
			}
			response.Success(c, http.StatusOK, result)
			return
		}

		contentType := c.GetHeader("Content-Type")
		if !strings.HasPrefix(contentType, "multipart/form-data") {
			// If neither multipart nor stored source requested, check if stored source was intended
			if sourceParam == "" && c.Request.ContentLength == 0 {
				result, err := svc.VerifyCandidate(c.Request.Context(), user, documentID, nil, 0, "", true)
				if err != nil {
					writeServiceError(c, err)
					return
				}
				response.Success(c, http.StatusOK, result)
				return
			}
			response.Error(c, http.StatusBadRequest, utils.CodeBadRequest, "Expected multipart/form-data with candidate file or source=stored query parameter")
			return
		}

		mr, err := c.Request.MultipartReader()
		if err != nil {
			response.Error(c, http.StatusBadRequest, utils.CodeBadRequest, "Invalid multipart/form-data stream")
			return
		}

		var (
			candidateFound bool
			result         *service.BlockchainVerifyResult
		)

		for {
			part, err := mr.NextPart()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				writeMultipartReadError(c, err)
				return
			}

			formName := part.FormName()
			if formName == "source" {
				buf := make([]byte, 64)
				n, _ := part.Read(buf)
				if strings.TrimSpace(strings.ToLower(string(buf[:n]))) == "stored" {
					sourceParam = "stored"
				}
				_ = part.Close()
				continue
			}

			if formName == "file" {
				candidateFound = true
				result, err = svc.VerifyCandidate(
					c.Request.Context(),
					user,
					documentID,
					part,
					52428800, // 50 MiB bounded
					part.FileName(),
					false,
				)
				_ = part.Close()
				if err != nil {
					writeServiceError(c, err)
					return
				}
				break
			}

			_ = part.Close()
		}

		if sourceParam == "stored" && !candidateFound {
			result, err = svc.VerifyCandidate(c.Request.Context(), user, documentID, nil, 0, "", true)
			if err != nil {
				writeServiceError(c, err)
				return
			}
			response.Success(c, http.StatusOK, result)
			return
		}

		if !candidateFound || result == nil {
			response.Error(c, http.StatusBadRequest, utils.CodeBadRequest, "No candidate file provided in multipart form")
			return
		}

		response.Success(c, http.StatusOK, result)
	}
}
