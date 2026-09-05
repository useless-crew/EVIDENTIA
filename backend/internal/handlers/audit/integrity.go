package audit

import (
	"net/http"

	"github.com/gin-gonic/gin"

	authpkg "evidentia/backend/internal/auth"
	"evidentia/backend/internal/service"
	"evidentia/backend/internal/utils"
	"evidentia/backend/pkg/response"
)

// integritySummaryResponse is GET /audit/integrity's response data shape.
type integritySummaryResponse = service.IntegritySummary

// Integrity handles GET /api/v1/audit/integrity — the dashboard's
// single at-a-glance summary call. Same authorization as the other System
// 11 audit-verification routes (audit:verify, ADMIN-only): this reports
// on the GLOBAL chain and verification history, the same "only makes
// sense against the complete, unfiltered view" reasoning
// StartVerification/Status/History already document.
//
// @Summary      Audit-chain integrity summary
// @Description  Cheap aggregate dashboard data: total audit entries, the current chain head (seq + hash), and the most recent verification run's status. Never runs a fresh verification itself. Requires audit:verify (ADMIN-only).
// @Tags         audit
// @Produce      json
// @Security     BearerAuth
// @Success      200  {object}  response.Envelope{data=integritySummaryResponse}
// @Failure      401  {object}  response.Envelope  "Authentication required"
// @Failure      403  {object}  response.Envelope  "You do not have permission to perform this action"
// @Router       /api/v1/audit/integrity [get]
func Integrity(svc *service.AuditService) gin.HandlerFunc {
	return func(c *gin.Context) {
		user, ok := authpkg.CurrentUser(c)
		if !ok {
			response.Error(c, http.StatusUnauthorized, utils.CodeUnauthorized, "Authentication required")
			return
		}

		result, err := svc.GetIntegritySummary(c.Request.Context(), user)
		if err != nil {
			writeServiceError(c, err)
			return
		}

		response.Success(c, http.StatusOK, result)
	}
}
