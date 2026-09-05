package audit

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	auditpkg "evidentia/backend/internal/audit"
	authpkg "evidentia/backend/internal/auth"
	"evidentia/backend/internal/realtime"
	"evidentia/backend/internal/service"
	"evidentia/backend/internal/utils"
	"evidentia/backend/pkg/response"
)

// Events handles GET /api/v1/audit/verify-chain/:verificationId/events —
// a Server-Sent Events stream of one verification's live progress. Same
// authorization as every other System 11 route (audit:verify, ADMIN-only,
// re-checked by GetVerification below) — the verification_id in the URL
// is NEVER treated as proof of authorization by itself (master prompt:
// "do not trust verification_id as proof of authorization"): this handler
// calls the SAME AuditService.GetVerification RBAC+RLS-checked read every
// REST caller goes through, and no data is EVER written to the client
// (not even the initial snapshot) until that check passes, so an
// unauthorized caller (or one naming an ID that exists but they cannot
// see — impossible in practice today since only ADMIN can ever create
// one, but the check exists structurally regardless) gets the identical
// 401/403/404 the plain status endpoint would, and is never upgraded to
// a stream at all. This handler subscribes to the broadcaster BEFORE
// running that check purely to avoid a completion-delivery race (see the
// Subscribe call below's own comment) — subscribing is not itself a data
// disclosure.
//
// @Summary      Stream an audit-chain verification's progress (SSE)
// @Description  text/event-stream of verification_started/verification_progress/verification_completed/verification_integrity_failure/verification_failed events for one verification run. Sends the CURRENT state immediately on connect (so a reconnecting client is never left waiting on the next event), then relays further events until a terminal one is sent or the client disconnects. Requires the same Authorization header as any other route — no token in the URL. Requires audit:verify (ADMIN-only).
// @Tags         audit
// @Produce      text/event-stream
// @Security     BearerAuth
// @Param        verificationId  path  string  true  "Verification ID (UUID)"
// @Success      200  {string}  string  "text/event-stream"
// @Failure      400  {object}  response.Envelope  "Invalid verification ID"
// @Failure      401  {object}  response.Envelope  "Authentication required"
// @Failure      403  {object}  response.Envelope  "You do not have permission to perform this action"
// @Failure      404  {object}  response.Envelope  "Verification not found"
// @Router       /api/v1/audit/verify-chain/{verificationId}/events [get]
func Events(svc *service.AuditService) gin.HandlerFunc {
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

		// Subscribe BEFORE the authorization/snapshot read below, not after:
		// Subscribe only registers an in-memory channel keyed by id — it
		// sends the caller no data by itself, so doing it first leaks
		// nothing. Doing it in the OTHER order (read-then-subscribe) loses
		// events for a verification that reaches its terminal state (and
		// therefore publishes its one and only completion event) in the gap
		// between the read and the Subscribe call — a real race for a
		// small/fast chain, where a background verification can complete in
		// well under the time this handler's own authorization DB round trip
		// takes. Subscribing first guarantees any concurrent completion is
		// either already reflected in the read below (already-terminal
		// initial event) or captured by this channel (delivered as a normal
		// subsequent event) — never both missed.
		ch, unsubscribe := svc.Broadcaster().Subscribe(id)

		// The authorization check every other route makes — see this
		// function's own doc comment for why this must still happen before
		// any data is ever written to the client.
		detail, err := svc.GetVerification(c.Request.Context(), user, id)
		if err != nil {
			unsubscribe()
			writeServiceError(c, err)
			return
		}

		realtime.StreamVerification(c, verificationEventFromDetail(*detail), ch, unsubscribe)
	}
}

// verificationEventFromDetail builds the initial SSE snapshot from an
// already-authorized REST read — see realtime.VerificationEvent's own doc
// comment for why this must carry the identical fields the REST endpoint
// itself returns.
func verificationEventFromDetail(d service.VerificationDetail) realtime.VerificationEvent {
	eventType := realtime.EventVerificationProgress
	switch d.Status {
	case auditpkg.VerificationStatusVerified:
		eventType = realtime.EventVerificationCompleted
	case auditpkg.VerificationStatusIntegrityFailure:
		eventType = realtime.EventVerificationIntegrityFailure
	case auditpkg.VerificationStatusFailed:
		eventType = realtime.EventVerificationFailed
	}

	return realtime.VerificationEvent{
		Type:           eventType,
		VerificationID: d.ID,
		Status:         d.Status,
		EntriesChecked: d.EntriesChecked,
		TotalEntries:   d.TotalEntries,
		ProgressPct:    d.ProgressPercent,
		FailedEntryID:  d.FailedEntryID,
		FailureType:    d.FailureType,
		FailureReason:  d.FailureReason,
		Timestamp:      time.Now().UTC(),
	}
}
