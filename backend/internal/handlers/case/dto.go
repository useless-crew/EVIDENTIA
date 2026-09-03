// Package cases implements the /api/v1/cases HTTP handlers: parse/
// validate the request, obtain the already-authenticated (and, for
// resource-scoped routes, already-authorized — see
// internal/middleware.RequirePermission/RequireCaseAccess in
// internal/httpserver/router.go) caller, delegate to
// internal/service.CaseService, shape the response. No SQL, transaction,
// role matrix, or audit write happens in this package directly (master
// prompt §37) — that is the service's job.
package cases

import (
	"encoding/json"
	"net/http"

	"github.com/gin-gonic/gin"

	"evidentia/backend/internal/service"
	"evidentia/backend/internal/utils"
	"evidentia/backend/pkg/response"
)

// createCaseRequest is POST /cases's request body. Deliberately has no id/
// created_by/created_at field — the client cannot supply them, not merely
// "should not" (master prompt §5): those are server-controlled and this
// struct has no field a client-sent value could even bind into.
type createCaseRequest struct {
	CaseNumber  string          `json:"case_number" binding:"required,min=1,max=100"`
	Title       string          `json:"title" binding:"required,min=1,max=255"`
	Description *string         `json:"description" binding:"omitempty,max=10000"`
	Status      *string         `json:"status" binding:"omitempty,max=50"`
	Metadata    json.RawMessage `json:"metadata"`
}

// updateCaseRequest is PUT /cases/:id's request body — a full replacement
// of every mutable field (see service.UpdateCaseInput's doc comment).
type updateCaseRequest struct {
	Title       string          `json:"title" binding:"required,min=1,max=255"`
	Description *string         `json:"description" binding:"omitempty,max=10000"`
	Status      string          `json:"status" binding:"required,max=50"`
	Metadata    json.RawMessage `json:"metadata"`
}

// caseListResponse/caseDetailResponse are thin aliases over the service's
// own response-shaped DTOs (service.CaseListResult/CaseDetail) — this
// package returns them directly rather than re-mapping field-by-field,
// since those types were already designed as the API response shape
// (never a bare generated.Case), not an internal domain model.
type caseListResponse = service.CaseListResult
type caseDetailResponse = service.CaseDetail

// writeServiceError renders err through the standard envelope, matching
// internal/handlers/auth's helper of the same name/behavior exactly: any
// *utils.AppError is rendered with its own status/code/public message; any
// other error is treated as an unexpected internal failure with a safe,
// generic message (never raw error text, which could contain SQL/driver
// detail).
func writeServiceError(c *gin.Context, err error) {
	if appErr, ok := utils.AsAppError(err); ok {
		response.FromAppError(c, appErr)
		return
	}
	response.Error(c, http.StatusInternalServerError, utils.CodeInternal, "An unexpected error occurred")
}
