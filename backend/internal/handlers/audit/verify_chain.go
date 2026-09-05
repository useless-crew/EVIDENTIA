package audit

import (
	"net/http"

	"github.com/gin-gonic/gin"

	authpkg "evidentia/backend/internal/auth"
	"evidentia/backend/internal/service"
	"evidentia/backend/internal/utils"
	"evidentia/backend/pkg/response"
)

// startVerificationResponse is POST /audit/verify-chain's response data
// shape (System 11 — asynchronous; see service.AuditService.
// StartVerification's own doc comment for the full design).
type startVerificationResponse = service.StartVerificationResult

// VerifyChain handles POST /api/v1/audit/verify-chain. Registered behind
// middleware.Auth and middleware.RequirePermission(authz.ActionAuditVerify)
// (ADMIN-only per the seed data) — service.AuditService.StartVerification
// independently re-checks this. Dispatches a background verification job
// (System 11) rather than verifying synchronously within this request —
// see docs/AUDIT_CHAIN.md's "Asynchronous Verification" for why a chain of
// any size must never require one long-running HTTP call. Two concurrent
// callers while a verification is already QUEUED/RUNNING both receive
// THAT SAME run's id, never two independent full-chain scans.
//
// @Summary      Start an audit-chain verification
// @Description  Dispatches a background job that walks the ENTIRE audit hash chain from genesis, verifying every entry's previous_hash linkage and recomputed SHA-256 entry_hash. Always 202: this call only ACCEPTS the request — see GET /audit/verify-chain/{verificationId} or the SSE events route for the actual outcome (VERIFIED/INTEGRITY_FAILURE/FAILED). If a verification is already QUEUED or RUNNING, that existing run's id/status is returned instead of starting a duplicate. Requires audit:verify (ADMIN-only).
// @Tags         audit
// @Produce      json
// @Security     BearerAuth
// @Success      202  {object}  response.Envelope{data=startVerificationResponse}
// @Failure      401  {object}  response.Envelope  "Authentication required"
// @Failure      403  {object}  response.Envelope  "You do not have permission to perform this action"
// @Router       /api/v1/audit/verify-chain [post]
func VerifyChain(svc *service.AuditService) gin.HandlerFunc {
	return func(c *gin.Context) {
		user, ok := authpkg.CurrentUser(c)
		if !ok {
			response.Error(c, http.StatusUnauthorized, utils.CodeUnauthorized, "Authentication required")
			return
		}

		result, err := svc.StartVerification(c.Request.Context(), user)
		if err != nil {
			writeServiceError(c, err)
			return
		}

		response.Success(c, http.StatusAccepted, result)
	}
}
