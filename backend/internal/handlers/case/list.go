package cases

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	authpkg "evidentia/backend/internal/auth"
	"evidentia/backend/internal/service"
	"evidentia/backend/internal/utils"
	"evidentia/backend/pkg/response"
)

// List handles GET /api/v1/cases. Registered behind middleware.Auth and
// middleware.RequirePermission(authz.ActionCaseRead) — the returned set is
// then scoped per-role by service.CaseService.ListCases via PostgreSQL RLS
// (master prompt §8/§9/§29), not by a per-item ABAC check in this
// handler or an in-memory filter here.
//
// @Summary      List cases
// @Description  Returns the caller's authorized, filtered, paginated case list — role-scoped by PostgreSQL RLS (POLICE/LAWYER/FORENSICS/JUDGE see cases they created or hold an active case_members row for; ADMIN sees all).
// @Tags         cases
// @Produce      json
// @Security     BearerAuth
// @Param        status        query     string  false  "Filter by exact status (OPEN, UNDER_INVESTIGATION, SUBMITTED, UNDER_REVIEW, CLOSED, ARCHIVED)"
// @Param        case_number   query     string  false  "Filter by case_number (substring match)"
// @Param        title         query     string  false  "Filter by title (substring match)"
// @Param        created_by    query     string  false  "Filter by creator user ID (UUID)"
// @Param        created_from  query     string  false  "Filter: created at or after this RFC3339 timestamp"
// @Param        created_to    query     string  false  "Filter: created before this RFC3339 timestamp"
// @Param        page          query     int     false  "Page number, default 1"
// @Param        page_size     query     int     false  "Rows per page, default 20, max 100"
// @Success      200  {object}  response.Envelope{data=caseListResponse}
// @Failure      400  {object}  response.Envelope  "Invalid filter value"
// @Failure      401  {object}  response.Envelope  "Authentication required"
// @Failure      403  {object}  response.Envelope  "You do not have permission to perform this action"
// @Router       /api/v1/cases [get]
func List(svc *service.CaseService) gin.HandlerFunc {
	return func(c *gin.Context) {
		user, ok := authpkg.CurrentUser(c)
		if !ok {
			response.Error(c, http.StatusUnauthorized, utils.CodeUnauthorized, "Authentication required")
			return
		}

		filter, err := parseCaseListFilter(c)
		if err != nil {
			response.Error(c, http.StatusBadRequest, utils.CodeBadRequest, err.Error())
			return
		}

		page := utils.ParsePagination(parseInt32(c.Query("page")), parseInt32(c.Query("page_size")))

		result, err := svc.ListCases(c.Request.Context(), user, filter, page)
		if err != nil {
			writeServiceError(c, err)
			return
		}

		response.Success(c, http.StatusOK, result)
	}
}

func parseCaseListFilter(c *gin.Context) (service.CaseListFilter, error) {
	var filter service.CaseListFilter

	if v := c.Query("status"); v != "" {
		filter.Status = &v
	}
	if v := c.Query("case_number"); v != "" {
		filter.CaseNumber = &v
	}
	if v := c.Query("title"); v != "" {
		filter.Title = &v
	}
	if v := c.Query("created_by"); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			return filter, errInvalidFilter("created_by must be a valid UUID")
		}
		filter.CreatedBy = &id
	}
	if v := c.Query("created_from"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			return filter, errInvalidFilter("created_from must be an RFC3339 timestamp")
		}
		filter.CreatedFrom = &t
	}
	if v := c.Query("created_to"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			return filter, errInvalidFilter("created_to must be an RFC3339 timestamp")
		}
		filter.CreatedTo = &t
	}

	return filter, nil
}

type invalidFilterError string

func (e invalidFilterError) Error() string { return string(e) }

func errInvalidFilter(msg string) error { return invalidFilterError(msg) }

// parseInt32 returns 0 (== "not supplied", see utils.ParsePagination) for
// an empty or malformed value rather than erroring — pagination
// parameters are a convenience, not a security boundary that needs to
// reject malformed input outright (matches this project's existing
// shape-only validation convention).
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
