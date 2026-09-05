package sse

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"evidentia/backend/internal/events"
)

// heartbeatInterval keeps intermediate proxies/load balancers from timing
// out an idle SSE connection between real events, and lets Stream notice
// a dead client connection promptly via c.Request.Context() even when no
// event has been published in a while.
//
// maxConnectionDuration bounds how long any single SSE connection may
// stay open before Stream forces it closed, requiring the client to
// reconnect — for a naturally-terminating stream (e.g. one audit
// verification run) this virtually never fires; for an otherwise-endless
// resource stream (e.g. a case's event feed) this is what forces a
// periodic reconnect, and therefore a periodic RE-authorization (the
// handler's own authz check runs again on every new connection) — so a
// user whose access to a resource is revoked mid-connection is never
// left subscribed to it indefinitely.
const (
	heartbeatInterval     = 15 * time.Second
	maxConnectionDuration = 1 * time.Hour
)

// Stream writes initial immediately, if non-nil (so a client — including
// one reconnecting after a missed event — gets the CURRENT state the
// instant it connects, never waiting on the next dispatch), then relays
// every subsequent event from ch until either isTerminal reports true for
// some received event, maxConnectionDuration elapses, or the client
// disconnects. unsubscribe (from Manager.Register) is always called
// exactly once, via defer, regardless of which exit path is taken — the
// caller must not also call it.
//
// isTerminal may be nil for an endless resource stream (a case's event
// feed has no single event that means "this stream is done" — it ends
// only on disconnect/max-duration/server-shutdown). For a stream that
// DOES represent one bounded operation (System 11's per-verification
// stream), the caller supplies a predicate recognizing that operation's
// own terminal event types — this package itself hardcodes no specific
// event type as terminal, keeping business/domain knowledge out of this
// transport layer.
//
// This function owns no PostgreSQL transaction, no Redis subscription,
// and no reference to whatever published the events it relays: it is
// handed a read-only channel and one already-authorized snapshot, and its
// only job is turning those into a correctly-framed text/event-stream
// response — the same decoupling between "whatever publishes events" and
// "the SSE connection" that System 11 already established, generalized.
func Stream(c *gin.Context, initial *events.Event, ch <-chan events.Event, unsubscribe func(), isTerminal func(events.Event) bool) {
	defer unsubscribe()

	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	// Disables response buffering on nginx-fronted deployments — without
	// this an SSE stream can sit fully buffered and never reach the client
	// until the connection closes, defeating the entire point of a
	// progress/notification stream.
	c.Header("X-Accel-Buffering", "no")

	// Flush the status line/headers to the client immediately, even when
	// initial is nil (an endless resource stream with no "current state"
	// snapshot to send — e.g. a case's event feed): Go's http.ResponseWriter
	// otherwise buffers headers until the first Write, which without this
	// WriteHeader/Flush would not happen until the FIRST heartbeat (up to
	// heartbeatInterval later) — leaving a connecting client's own
	// fetch()/EventSource call blocked, not yet even knowing the connection
	// was accepted, for no good reason.
	c.Writer.WriteHeader(http.StatusOK)
	c.Writer.Flush()

	if initial != nil {
		if !writeEvent(c, *initial) {
			return
		}
		if isTerminal != nil && isTerminal(*initial) {
			return
		}
	}

	heartbeat := time.NewTicker(heartbeatInterval)
	defer heartbeat.Stop()

	maxDuration := time.NewTimer(maxConnectionDuration)
	defer maxDuration.Stop()

	for {
		select {
		case <-c.Request.Context().Done():
			// Client disconnected (navigated away, closed the tab, network
			// drop) — nothing further to send; unsubscribe (deferred above)
			// releases this connection's Manager registration.
			return
		case <-maxDuration.C:
			// Force a reconnect — see this file's own doc comment on
			// maxConnectionDuration for why.
			return
		case event, ok := <-ch:
			if !ok {
				return
			}
			if !writeEvent(c, event) {
				return
			}
			if isTerminal != nil && isTerminal(event) {
				return
			}
		case <-heartbeat.C:
			// A bare SSE comment line — never parsed as a data event by any
			// conforming client, purely a keep-alive signal. Never itself an
			// events.Event, never audited, never published to Redis (master
			// prompt: "must not overload Redis" / "must not be audited").
			if _, err := c.Writer.Write([]byte(": heartbeat\n\n")); err != nil {
				return
			}
			c.Writer.Flush()
		}
	}
}

// writeEvent frames one events.Event as `id: <event_id>\nevent:
// <event_type>\ndata: <json>\n\n` (the standard SSE wire format,
// including the optional `id:` line for Last-Event-ID correlation — see
// docs/REALTIME_EVENTS.md's "Reconnection" for why this package does NOT
// implement replay against it) and flushes it immediately — buffering
// would defeat a progress/notification stream. Returns false on a write
// error (client gone), signaling the caller to stop.
func writeEvent(c *gin.Context, event events.Event) bool {
	payload, err := json.Marshal(event)
	if err != nil {
		return false
	}
	if _, err := c.Writer.Write([]byte("id: " + event.EventID.String() + "\nevent: " + event.EventType + "\ndata: ")); err != nil {
		return false
	}
	if _, err := c.Writer.Write(payload); err != nil {
		return false
	}
	if _, err := c.Writer.Write([]byte("\n\n")); err != nil {
		return false
	}
	c.Writer.Flush()
	return true
}
