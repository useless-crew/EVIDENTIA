package audit

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	authpkg "evidentia/backend/internal/auth"
	"evidentia/backend/internal/service"
	"evidentia/backend/internal/utils"
	"evidentia/backend/pkg/response"
)

// verifyChainResponse is POST /audit/verify-chain's response data shape.
type verifyChainResponse = service.ChainVerificationResult

// VerifyChain handles POST /api/v1/audit/verify-chain. Registered behind
// middleware.Auth and middleware.RequirePermission(authz.ActionAuditVerify)
// (ADMIN-only per the seed data) — service.AuditService.VerifyChain
// independently re-checks this. Synchronous: for a chain small enough to
// finish within maxVerifyEntriesPerRequest (see that constant's doc
// comment), the response is final (status VERIFIED or
// INTEGRITY_FAILURE, next_seq absent). For a larger chain, next_seq is
// present — resume by calling again with from_seq=next_seq; entries
// already confirmed valid in an earlier call are never silently trusted
// without being part of SOME call's own verified batch (each call's own
// EntriesChecked/Status only describes what THAT call itself checked).
//
// @Summary      Verify the audit hash chain
// @Description  Walks the audit trail from from_seq (default 0 = genesis) in bounded-size batches, recomputing and comparing each entry's hash and prev_hash linkage. VERIFIED and INTEGRITY_FAILURE are both a 200 — verification answering the question is success either way. On failure, returns the failing entry's ID/seq and the expected vs. actual hashes (never metadata content or secrets). Requires audit:verify (ADMIN-only).
// @Tags         audit
// @Produce      json
// @Security     BearerAuth
// @Param        from_seq     query     int  false  "Resume verification just after this seq (default 0 — start from genesis)"
// @Param        max_entries  query     int  false  "Maximum entries to check in this call (default/cap: a large internal limit — see next_seq for resuming)"
// @Success      200  {object}  response.Envelope{data=verifyChainResponse}  "Always 200 on a completed batch — see status: VERIFIED or INTEGRITY_FAILURE, and next_seq if more remain"
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

		fromSeq, err := parseFromSeq(c.Query("from_seq"))
		if err != nil {
			response.Error(c, http.StatusBadRequest, utils.CodeBadRequest, "from_seq must be a non-negative integer")
			return
		}
		maxEntries := parseInt32(c.Query("max_entries"))

		result, err := svc.VerifyChain(c.Request.Context(), user, fromSeq, maxEntries)
		if err != nil {
			writeServiceError(c, err)
			return
		}

		response.Success(c, http.StatusOK, result)
	}
}

func parseFromSeq(v string) (int64, error) {
	if v == "" {
		return 0, nil
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n < 0 {
		return 0, errInvalidFilter("from_seq must be a non-negative integer")
	}
	return n, nil
}
