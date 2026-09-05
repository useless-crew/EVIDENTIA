package audit

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	auditpkg "evidentia/backend/internal/audit"
	authpkg "evidentia/backend/internal/auth"
	"evidentia/backend/internal/events"
	"evidentia/backend/internal/service"
	"evidentia/backend/internal/sse"
	"evidentia/backend/internal/utils"
	"evidentia/backend/pkg/response"
)

// Events handles GET /api/v1/audit/verify-chain/:verificationId/events —
// a Server-Sent Events stream of one verification's live progress, built
// on System 13's shared internal/sse.Manager/internal/events
// infrastructure (the SAME infrastructure any future resource-scoped SSE
// route uses — see internal/handlers/case's Events for the other current
// consumer). Same authorization as every other System 11 route
// (audit:verify, ADMIN-only, re-checked by GetVerification below) — the
// verification_id in the URL is NEVER treated as proof of authorization by
// itself (master prompt: "do not trust verification_id as proof of
// authorization"): this handler calls the SAME AuditService.
// GetVerification RBAC+RLS-checked read every REST caller goes through,
// and no data is EVER written to the client (not even the initial
// snapshot) until that check passes, so an unauthorized caller (or one
// naming an ID that exists but they cannot see — impossible in practice
// today since only ADMIN can ever create one, but the check exists
// structurally regardless) gets the identical 401/403/404 the plain
// status endpoint would, and is never upgraded to a stream at all. This
// handler registers with the Manager BEFORE running that check purely to
// avoid a completion-delivery race (see the Register call below's own
// comment) — registering is not itself a data disclosure.
//
// @Summary      Stream an audit-chain verification's progress (SSE)
// @Description  text/event-stream of AUDIT_VERIFICATION_STARTED/AUDIT_VERIFICATION_PROGRESS/AUDIT_VERIFICATION_COMPLETED/AUDIT_INTEGRITY_FAILURE/AUDIT_VERIFICATION_FAILED events for one verification run. Sends the CURRENT state immediately on connect (so a reconnecting client is never left waiting on the next event), then relays further events until a terminal one is sent or the client disconnects. Requires the same Authorization header as any other route — no token in the URL. Requires audit:verify (ADMIN-only).
// @Tags         audit
// @Produce      text/event-stream
// @Security     BearerAuth
// @Param        verificationId  path  string  true  "Verification ID (UUID)"
// @Success      200  {string}  string  "text/event-stream"
// @Failure      400  {object}  response.Envelope  "Invalid verification ID"
// @Failure      401  {object}  response.Envelope  "Authentication required"
// @Failure      403  {object}  response.Envelope  "You do not have permission to perform this action"
// @Failure      404  {object}  response.Envelope  "Verification not found"
// @Failure      429  {object}  response.Envelope  "Too many concurrent event streams for this user"
// @Router       /api/v1/audit/verify-chain/{verificationId}/events [get]
func Events(svc *service.AuditService, manager *sse.Manager) gin.HandlerFunc {
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

		// Register BEFORE the authorization/snapshot read below, not after:
		// Register only records an in-memory channel keyed by scope — it
		// sends the caller no data by itself, so doing it first leaks
		// nothing. Doing it in the OTHER order (read-then-register) loses
		// events for a verification that reaches its terminal state (and
		// therefore publishes its one and only completion event) in the gap
		// between the read and the Register call — a real race for a
		// small/fast chain, where a background verification can complete in
		// well under the time this handler's own authorization DB round trip
		// takes. Registering first guarantees any concurrent completion is
		// either already reflected in the read below (already-terminal
		// initial event) or captured by this channel (delivered as a normal
		// subsequent event) — never both missed.
		ch, unsubscribe, err := manager.Register(user.ID, events.ScopeKey(events.ResourceTypeAuditVerification, id.String()))
		if err != nil {
			response.Error(c, http.StatusTooManyRequests, utils.CodeTooManyRequests, "Too many concurrent event streams for this user")
			return
		}

		// The authorization check every other route makes — see this
		// function's own doc comment for why this must still happen before
		// any data is ever written to the client.
		detail, err := svc.GetVerification(c.Request.Context(), user, id)
		if err != nil {
			unsubscribe()
			writeServiceError(c, err)
			return
		}

		initial := auditVerificationEventFromDetail(*detail)
		sse.Stream(c, &initial, ch, unsubscribe, isAuditVerificationTerminal)
	}
}

// isAuditVerificationTerminal recognizes the three event types that end
// one verification run's own stream — see sse.Stream's own doc comment
// for why this predicate lives in THIS handler (domain knowledge of
// "what does completion look like for an audit verification"), not in
// the generic transport package.
func isAuditVerificationTerminal(e events.Event) bool {
	switch e.EventType {
	case events.TypeAuditVerificationCompleted, events.TypeAuditIntegrityFailure, events.TypeAuditVerificationFailed:
		return true
	default:
		return false
	}
}

// auditVerificationEventFromDetail builds the initial SSE snapshot from an
// already-authorized REST read — see events.AuditVerificationData's own
// doc comment for why this must carry the identical fields the REST
// endpoint itself returns.
func auditVerificationEventFromDetail(d service.VerificationDetail) events.Event {
	eventType := events.TypeAuditVerificationProgress
	switch d.Status {
	case auditpkg.VerificationStatusVerified:
		eventType = events.TypeAuditVerificationCompleted
	case auditpkg.VerificationStatusIntegrityFailure:
		eventType = events.TypeAuditIntegrityFailure
	case auditpkg.VerificationStatusFailed:
		eventType = events.TypeAuditVerificationFailed
	}

	var failedEntryID *string
	if d.FailedEntryID != nil {
		id := d.FailedEntryID.String()
		failedEntryID = &id
	}
	data, _ := json.Marshal(events.AuditVerificationData{
		VerificationID: d.ID.String(),
		Status:         d.Status,
		EntriesChecked: d.EntriesChecked,
		TotalEntries:   d.TotalEntries,
		ProgressPct:    d.ProgressPercent,
		FailedEntryID:  failedEntryID,
		FailureType:    d.FailureType,
		FailureReason:  d.FailureReason,
	})

	return events.Event{
		EventID:      uuid.New(),
		EventType:    eventType,
		EventVersion: events.CurrentEventVersion,
		Timestamp:    time.Now().UTC(),
		ResourceType: events.ResourceTypeAuditVerification,
		ResourceID:   d.ID.String(),
		Data:         data,
	}
}
