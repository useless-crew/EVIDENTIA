// Package audit implements the audit-trail HTTP handlers: parse/validate
// the request, obtain the already-authenticated caller, delegate to
// internal/service.AuditService, shape the response. No SQL, hashing,
// transaction, or canonicalization happens in this package directly —
// that is AuditService's (and, beneath it, internal/audit's) job.
package audit

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"evidentia/backend/internal/utils"
	"evidentia/backend/pkg/response"
)

func writeServiceError(c *gin.Context, err error) {
	if appErr, ok := utils.AsAppError(err); ok {
		response.FromAppError(c, appErr)
		return
	}
	response.Error(c, http.StatusInternalServerError, utils.CodeInternal, "An unexpected error occurred")
}

// parseInt32 returns 0 (== "not supplied") for an empty or malformed
// value rather than erroring — pagination is a convenience, not a
// security boundary (same convention as internal/handlers/case's own
// parseInt32).
func parseInt32(v string) int32 {
	if v == "" {
		return 0
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return 0
	}
	return int32(n)
}
