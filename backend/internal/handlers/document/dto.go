// Package document implements the document-management HTTP handlers:
// parse/validate the request, obtain the already-authenticated (and, for
// resource-scoped routes, already-authorized — see
// internal/middleware.RequireCaseAccess/RequireDocumentAccess in
// internal/httpserver/router.go) caller, delegate to
// internal/service.DocumentService, shape the response. No SQL, object-
// storage SDK call, hashing, transaction, or audit write happens in this
// package directly (master prompt §37) — that is the service's job.
package document

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"evidentia/backend/internal/service"
	"evidentia/backend/internal/utils"
	"evidentia/backend/pkg/response"
)

// documentResponse is a thin alias over the service's own response-shaped
// DTO (service.DocumentSummary) — returned directly rather than re-mapped
// field-by-field, since it was already designed as the API response shape
// (never a bare generated.Document, which would leak storage_bucket/
// storage_object_key).
type documentResponse = service.DocumentSummary

// writeServiceError renders err through the standard envelope, matching
// internal/handlers/{auth,case}'s helper of the same name/behavior: any
// *utils.AppError is rendered with its own status/code/public message; any
// other error is treated as an unexpected internal failure with a safe,
// generic message.
func writeServiceError(c *gin.Context, err error) {
	if appErr, ok := utils.AsAppError(err); ok {
		response.FromAppError(c, appErr)
		return
	}
	response.Error(c, http.StatusInternalServerError, utils.CodeInternal, "An unexpected error occurred")
}
