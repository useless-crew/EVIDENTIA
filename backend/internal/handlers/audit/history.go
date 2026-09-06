package audit

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

// verificationHistoryResponse is GET /audit/verifications' response data
// shape.
type verificationHistoryResponse = service.VerificationListResult

// History handles GET /api/v1/audit/verifications. Same authorization as
// the other System 11 audit-verification routes (audit:verify,
// ADMIN-only) — see status.go's doc comment for why this is enough on its
// own (RLS additionally scopes every row to ADMIN regardless).
//
// @Summary      List audit-chain verification history
// @Description  Returns the paginated history of every verification run — QUEUED/RUNNING/VERIFIED/INTEGRITY_FAILURE/FAILED. Requires audit:verify (ADMIN-only).
// @Tags         audit
// @Produce      json
// @Security     BearerAuth
// @Param        status           query  string  false  "Filter by exact status (QUEUED, RUNNING, VERIFIED, INTEGRITY_FAILURE, FAILED)"
// @Param        requested_by     query  string  false  "Filter by the user ID (UUID) who requested the run"
// @Param        from             query  string  false  "Filter: created at or after this RFC3339 timestamp"
// @Param        to               query  string  false  "Filter: created before this RFC3339 timestamp"
// @Param        page             query  int     false  "Page number, default 1"
// @Param        page_size        query  int     false  "Rows per page, default 20, max 100"
// @Success      200  {object}  response.Envelope{data=verificationHistoryResponse}
// @Failure      400  {object}  response.Envelope  "Invalid filter value"
// @Failure      401  {object}  response.Envelope  "Authentication required"
// @Failure      403  {object}  response.Envelope  "You do not have permission to perform this action"
// @Router       /api/v1/audit/verifications [get]
func History(svc *service.AuditService) gin.HandlerFunc {
	return func(c *gin.Context) {
		user, ok := authpkg.CurrentUser(c)
		if !ok {
			response.Error(c, http.StatusUnauthorized, utils.CodeUnauthorized, "Authentication required")
			return
		}

		filter, err := parseVerificationListFilter(c)
		if err != nil {
			response.Error(c, http.StatusBadRequest, utils.CodeBadRequest, err.Error())
			return
		}

		page := utils.ParsePagination(parseInt32(c.Query("page")), parseInt32(c.Query("page_size")))

		result, err := svc.ListVerifications(c.Request.Context(), user, filter, page)
		if err != nil {
			writeServiceError(c, err)
			return
		}

		response.Success(c, http.StatusOK, result)
	}
}

func parseVerificationListFilter(c *gin.Context) (service.VerificationListFilter, error) {
	var filter service.VerificationListFilter

	if v := c.Query("status"); v != "" {
		filter.Status = &v
	}
	if v := c.Query("requested_by"); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			return filter, errInvalidFilter("requested_by must be a valid UUID")
		}
		filter.RequestedBy = &id
	}
	if v := c.Query("from"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			return filter, errInvalidFilter("from must be an RFC3339 timestamp")
		}
		filter.From = &t
	}
	if v := c.Query("to"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			return filter, errInvalidFilter("to must be an RFC3339 timestamp")
		}
		filter.To = &t
	}

	return filter, nil
}
