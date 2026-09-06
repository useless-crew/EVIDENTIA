package audit

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	authpkg "evidentia/backend/internal/auth"
	"evidentia/backend/internal/service"
	"evidentia/backend/internal/utils"
	"evidentia/backend/pkg/response"
)

// verificationDetailResponse is GET /audit/verify-chain/:id's response
// data shape.
type verificationDetailResponse = service.VerificationDetail

// Status handles GET /api/v1/audit/verify-chain/:verificationId. Same
// authorization as POST /audit/verify-chain (audit:verify, ADMIN-only) —
// service.AuditService.GetVerification independently re-checks this and
// scopes the query through PostgreSQL RLS (audit_verifications_select),
// which is what actually prevents IDOR here: a non-ADMIN caller sees zero
// rows for ANY verification_id, valid or not, never distinguishing
// "exists but not yours" from "doesn't exist" (both surface as the exact
// same 404 GetVerification returns for pgx.ErrNoRows).
//
// @Summary      Get an audit-chain verification's current status
// @Description  Returns one verification run's full current state — status, progress, and (for INTEGRITY_FAILURE/FAILED) safe failure detail. A QUEUED/RUNNING run with no progress reported in longer than expected is reconciled to FAILED before being returned (see docs/AUDIT_CHAIN.md's "Stale Verification Recovery"). Requires audit:verify (ADMIN-only).
// @Tags         audit
// @Produce      json
// @Security     BearerAuth
// @Param        verificationId  path      string  true  "Verification ID (UUID)"
// @Success      200  {object}  response.Envelope{data=verificationDetailResponse}
// @Failure      400  {object}  response.Envelope  "Invalid verification ID"
// @Failure      401  {object}  response.Envelope  "Authentication required"
// @Failure      403  {object}  response.Envelope  "You do not have permission to perform this action"
// @Failure      404  {object}  response.Envelope  "Verification not found"
// @Router       /api/v1/audit/verify-chain/{verificationId} [get]
func Status(svc *service.AuditService) gin.HandlerFunc {
	return func(c *gin.Context) {
		user, ok := authpkg.CurrentUser(c)
		if !ok {
			response.Error(c, http.StatusUnauthorized, utils.CodeUnauthorized, "Authentication required")
			return
		}

		id, err := uuid.Parse(c.Param("verificationId"))
		if err != nil {
			response.Error(c, http.StatusBadRequest, utils.CodeBadRequest, "verificationId must be a valid UUID")
			return
		}

		result, err := svc.GetVerification(c.Request.Context(), user, id)
		if err != nil {
			writeServiceError(c, err)
			return
		}

		response.Success(c, http.StatusOK, result)
	}
}
