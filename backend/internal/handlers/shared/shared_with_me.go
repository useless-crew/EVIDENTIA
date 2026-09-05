// Package shared implements the cross-document "Shared With Me" route
// (master prompt §59) — GET /api/v1/shared/documents. Deliberately its
// own top-level route (not nested under /documents/:id) since it is not
// scoped to any single document: it lists every document CURRENTLY
// shared with the caller, across every case. No SQL/authorization logic
// lives here — that is service.ShareService.ListSharedWithMe's job.
package shared

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	authpkg "evidentia/backend/internal/auth"
	"evidentia/backend/internal/service"
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

// parseInt32 returns 0 (== "not supplied", see utils.ParsePagination) for
// an empty or malformed value — pagination is a convenience, not a
// security boundary (same convention as internal/handlers/case.parseInt32).
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

// SharedWithMe handles GET /api/v1/shared/documents. Registered behind
// middleware.Auth only — every authenticated user may list their OWN
// incoming shares; there is no separate RBAC/ABAC gate beyond "this share
// row names you as recipient and is currently active", which
// service.ShareService.ListSharedWithMe's query itself already
// enforces (backed by documents_select's own RLS delegated-access
// branch — see db/migrations/000004_document_sharing.up.sql).
//
// @Summary      List documents shared with the caller
// @Description  Every document for which the caller currently holds an ACTIVE, unexpired share, newest share first. Never includes a document through case membership alone — this is delegated access only.
// @Tags         documents
// @Produce      json
// @Security     BearerAuth
// @Param        page       query     int  false  "Page number, default 1"
// @Param        page_size  query     int  false  "Rows per page, default 20, max 100"
// @Success      200  {object}  response.Envelope{data=service.SharedWithMeResult}
// @Failure      401  {object}  response.Envelope  "Authentication required"
// @Router       /api/v1/shared/documents [get]
func SharedWithMe(svc *service.ShareService) gin.HandlerFunc {
	return func(c *gin.Context) {
		user, ok := authpkg.CurrentUser(c)
		if !ok {
			response.Error(c, http.StatusUnauthorized, utils.CodeUnauthorized, "Authentication required")
			return
		}

		page := utils.ParsePagination(parseInt32(c.Query("page")), parseInt32(c.Query("page_size")))

		result, err := svc.ListSharedWithMe(c.Request.Context(), user, page)
		if err != nil {
			writeServiceError(c, err)
			return
		}

		response.Success(c, http.StatusOK, result)
	}
}
