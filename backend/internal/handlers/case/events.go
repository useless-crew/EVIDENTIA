package cases

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	authpkg "evidentia/backend/internal/auth"
	"evidentia/backend/internal/events"
	"evidentia/backend/internal/sse"
	"evidentia/backend/internal/utils"
	"evidentia/backend/pkg/response"
)

// Events handles GET /api/v1/cases/:id/events — a Server-Sent Events
// notification stream for one case, built on System 13's shared
// internal/sse.Manager/internal/events infrastructure (the SAME
// infrastructure internal/handlers/audit's Events route uses — see that
// file for the other current consumer). Registered behind
// middleware.RequireCaseAccess(authz.ActionCaseRead, "id") — the SAME
// case:read ABAC check GET /cases/:id itself requires has already run by
// the time this handler executes; unlike the audit endpoint, this stream
// has no bounded "current state" to re-derive (it represents an ONGOING
// case, not one finished operation), so there is no second, service-layer
// re-check here — RequireCaseAccess re-running on every new connection
// (including the periodic forced reconnect internal/sse.Stream's own
// maxConnectionDuration causes) is what keeps a long-lived subscription's
// authorization from ever growing stale (see that constant's own doc
// comment).
//
// Publishes no initial snapshot (nil) and has no terminal event (nil
// predicate): this is an endless notification stream for as long as the
// case exists and the client stays connected — DOCUMENT_VERIFICATION_
// COMPLETED, CERTIFICATE_GENERATION_COMPLETED, DOCUMENT_REDACTION_
// COMPLETED, SHARE_CREATED, and SHARE_REVOKED are all published scoped to
// this case (see internal/events/catalog.go). A client MUST still treat
// any event as a mere "state may have changed" signal and refetch the
// authoritative REST resource it cares about — this stream itself never
// carries the full, current document/case state.
//
// @Summary      Stream a case's real-time notifications (SSE)
// @Description  text/event-stream of DOCUMENT_VERIFICATION_COMPLETED/CERTIFICATE_GENERATION_COMPLETED/DOCUMENT_REDACTION_COMPLETED/SHARE_CREATED/SHARE_REVOKED events for one case. Sends no initial snapshot — the client's existing REST queries remain the source of current state; this stream is a refetch signal only. Never closes on its own (aside from a periodic forced reconnect, at most hourly, that re-validates authorization) — it ends only on client disconnect or server shutdown. Requires the same Authorization header as any other route — no token in the URL. Requires case:read plus a relationship to this specific case.
// @Tags         cases
// @Produce      text/event-stream
// @Security     BearerAuth
// @Param        id   path  string  true  "Case ID (UUID)"
// @Success      200  {string}  string  "text/event-stream"
// @Failure      401  {object}  response.Envelope  "Authentication required"
// @Failure      403  {object}  response.Envelope  "You do not have permission to perform this action (also returned for a nonexistent case ID)"
// @Failure      429  {object}  response.Envelope  "Too many concurrent event streams for this user"
// @Router       /api/v1/cases/{id}/events [get]
func Events(manager *sse.Manager) gin.HandlerFunc {
	return func(c *gin.Context) {
		user, ok := authpkg.CurrentUser(c)
		if !ok {
			response.Error(c, http.StatusUnauthorized, utils.CodeUnauthorized, "Authentication required")
			return
		}

		caseID, err := uuid.Parse(c.Param("id"))
		if err != nil {
			// Identical to an authorized-but-nonexistent/unrelated case — see
			// case/get.go's own doc comment: RequireCaseAccess already denies
			// a malformed ID the same way before this handler is reached in
			// the normal request path.
			response.Error(c, http.StatusForbidden, utils.CodeForbidden, "You do not have permission to perform this action")
			return
		}

		ch, unsubscribe, err := manager.Register(user.ID, events.ScopeKey(events.ResourceTypeCase, caseID.String()))
		if err != nil {
			response.Error(c, http.StatusTooManyRequests, utils.CodeTooManyRequests, "Too many concurrent event streams for this user")
			return
		}

		sse.Stream(c, nil, ch, unsubscribe, nil)
	}
}
