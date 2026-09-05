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

// listResponse is GET /audit's response data shape.
type listResponse = service.AuditListResult

// List handles GET /api/v1/audit. Registered behind middleware.Auth only
// — service.AuditService.List performs the real authorization
// (audit:read RBAC) and row-level visibility is PostgreSQL RLS's job
// (audit_log_select), not a route-level ABAC middleware here (there is
// no single case/document ID in this route's URL to check against the
// way RequireCaseAccess/RequireDocumentAccess do for other resources —
// GET /audit is a filtered LISTING, exactly like GET /cases).
//
// @Summary      List audit trail entries
// @Description  Returns the caller's authorized, filtered, paginated audit trail — ADMIN sees every entry; every other role sees only their own actions plus entries tied to a case they are an active member of (PostgreSQL RLS, audit_log_select). A filter can only narrow this further, never widen it. Requires audit:read.
// @Tags         audit
// @Produce      json
// @Security     BearerAuth
// @Param        user_id        query     string  false  "Filter by actor user ID (UUID)"
// @Param        role           query     string  false  "Filter by the role recorded on the entry"
// @Param        action         query     string  false  "Filter by exact action (e.g. DOCUMENT_UPLOADED)"
// @Param        resource_type  query     string  false  "Filter by resource type (e.g. document, case, user)"
// @Param        resource_id    query     string  false  "Filter by resource ID (UUID)"
// @Param        case_id        query     string  false  "Filter by case ID (UUID)"
// @Param        from           query     string  false  "Filter: at or after this RFC3339 timestamp"
// @Param        to             query     string  false  "Filter: before this RFC3339 timestamp"
// @Param        page           query     int     false  "Page number, default 1"
// @Param        page_size      query     int     false  "Rows per page, default 20, max 100"
// @Success      200  {object}  response.Envelope{data=listResponse}
// @Failure      400  {object}  response.Envelope  "Invalid filter value"
// @Failure      401  {object}  response.Envelope  "Authentication required"
// @Failure      403  {object}  response.Envelope  "You do not have permission to perform this action"
// @Router       /api/v1/audit [get]
func List(svc *service.AuditService) gin.HandlerFunc {
	return func(c *gin.Context) {
		user, ok := authpkg.CurrentUser(c)
		if !ok {
			response.Error(c, http.StatusUnauthorized, utils.CodeUnauthorized, "Authentication required")
			return
		}

		filter, err := parseAuditListFilter(c)
		if err != nil {
			response.Error(c, http.StatusBadRequest, utils.CodeBadRequest, err.Error())
			return
		}

		page := utils.ParsePagination(parseInt32(c.Query("page")), parseInt32(c.Query("page_size")))

		result, err := svc.List(c.Request.Context(), user, filter, page)
		if err != nil {
			writeServiceError(c, err)
			return
		}

		response.Success(c, http.StatusOK, result)
	}
}

func parseAuditListFilter(c *gin.Context) (service.AuditListFilter, error) {
	var filter service.AuditListFilter

	if v := c.Query("user_id"); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			return filter, errInvalidFilter("user_id must be a valid UUID")
		}
		filter.UserID = &id
	}
	if v := c.Query("role"); v != "" {
		filter.Role = &v
	}
	if v := c.Query("action"); v != "" {
		filter.Action = &v
	}
	if v := c.Query("resource_type"); v != "" {
		filter.ResourceType = &v
	}
	if v := c.Query("resource_id"); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			return filter, errInvalidFilter("resource_id must be a valid UUID")
		}
		filter.ResourceID = &id
	}
	if v := c.Query("case_id"); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			return filter, errInvalidFilter("case_id must be a valid UUID")
		}
		filter.CaseID = &id
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

type invalidFilterError string

func (e invalidFilterError) Error() string { return string(e) }

func errInvalidFilter(msg string) error { return invalidFilterError(msg) }
