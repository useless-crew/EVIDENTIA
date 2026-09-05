package user

import (
	"net/http"

	"github.com/gin-gonic/gin"

	authpkg "evidentia/backend/internal/auth"
	"evidentia/backend/internal/events"
	"evidentia/backend/internal/sse"
	"evidentia/backend/internal/utils"
	"evidentia/backend/pkg/response"
)

// adminUsersScopeID mirrors internal/service.adminUsersScopeID exactly —
// admin user management is one global resource (see that constant's own
// doc comment), so this handler always registers for the SAME fixed
// scope regardless of caller, never a client-supplied ID.
const adminUsersScopeID = "global"

// Events handles GET /api/v1/admin/users/events — a Server-Sent Events
// notification stream for administrative user-management activity
// (USER_CREATED/USER_UPDATED/USER_ROLE_CHANGED/USER_ACTIVATED/
// USER_DEACTIVATED/USER_SUSPENDED — see internal/events/catalog.go),
// built on System 13's shared internal/sse.Manager/internal/events
// infrastructure — the SAME infrastructure internal/handlers/audit and
// internal/handlers/case's own Events routes use. Registered behind
// middleware.RequirePermission(authz.ActionUserRead) — the SAME RBAC gate
// GET /admin/users itself requires (ADMIN-only per the seed data); unlike
// the audit/case routes there is no additional per-resource ABAC check to
// re-run here, because admin user management has no per-resource
// ownership/membership concept at all — it is either globally visible to
// an authorized administrator, or not visible at all.
//
// Sends no initial snapshot (nil) and has no terminal event (nil
// predicate) — this is an endless notification stream for as long as the
// caller stays connected, exactly like GET /cases/:id/events. A client
// treats any event as a "the admin user list may have changed" signal
// and refetches GET /admin/users — this stream never carries the full,
// current user list itself.
//
// @Summary      Stream administrative user-management notifications (SSE)
// @Description  text/event-stream of USER_CREATED/USER_UPDATED/USER_ROLE_CHANGED/USER_ACTIVATED/USER_DEACTIVATED/USER_SUSPENDED events. Sends no initial snapshot — the client's existing GET /admin/users query remains the source of current state; this stream is a refetch signal only. Never closes on its own aside from a periodic forced reconnect (at most hourly) that re-validates authorization — it ends only on client disconnect or server shutdown. Requires the same Authorization header as any other route — no token in the URL. Requires user:read (ADMIN-only).
// @Tags         admin
// @Produce      text/event-stream
// @Security     BearerAuth
// @Success      200  {string}  string  "text/event-stream"
// @Failure      401  {object}  response.Envelope  "Authentication required"
// @Failure      403  {object}  response.Envelope  "You do not have permission to perform this action"
// @Failure      429  {object}  response.Envelope  "Too many concurrent event streams for this user"
// @Router       /api/v1/admin/users/events [get]
func Events(manager *sse.Manager) gin.HandlerFunc {
	return func(c *gin.Context) {
		actor, ok := authpkg.CurrentUser(c)
		if !ok {
			response.Error(c, http.StatusUnauthorized, utils.CodeUnauthorized, "Authentication required")
			return
		}

		ch, unsubscribe, err := manager.Register(actor.ID, events.ScopeKey(events.ResourceTypeAdminUsers, adminUsersScopeID))
		if err != nil {
			response.Error(c, http.StatusTooManyRequests, utils.CodeTooManyRequests, "Too many concurrent event streams for this user")
			return
		}

		sse.Stream(c, nil, ch, unsubscribe, nil)
	}
}
