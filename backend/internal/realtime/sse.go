package realtime

import (
	"encoding/json"
	"time"

	"github.com/gin-gonic/gin"
)

// heartbeatInterval keeps intermediate proxies/load balancers from timing
// out an idle SSE connection between real progress events, and lets the
// handler notice a dead client connection promptly via c.Request.Context()
// even when no VerificationEvent has been published in a while.
const heartbeatInterval = 15 * time.Second

// StreamVerification writes initial immediately (so a client — including
// one reconnecting after a missed event — always gets the CURRENT state
// the instant it connects, never waiting on the next Broadcaster.Publish
// call), then relays every subsequent event from ch until either a
// terminal event is sent (see VerificationEvent.Terminal) or the client
// disconnects (c.Request.Context().Done()). unsubscribe is always called
// exactly once, via defer, regardless of which exit path is taken — the
// caller must not also call it.
//
// This function owns no PostgreSQL transaction, no Asynq task, and no
// reference to the verification worker itself: it is handed a read-only
// channel and one already-fetched snapshot, and its only job is turning
// those into a correctly-framed text/event-stream response. This is the
// decoupling master prompt requires between "the verification worker" and
// "the SSE connection".
func StreamVerification(c *gin.Context, initial VerificationEvent, ch <-chan VerificationEvent, unsubscribe func()) {
	defer unsubscribe()

	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	// Disables response buffering on nginx-fronted deployments — without
	// this an SSE stream can sit fully buffered and never reach the
	// client until the connection closes, defeating the entire point of
	// a progress stream.
	c.Header("X-Accel-Buffering", "no")

	if !writeEvent(c, initial) {
		return
	}
	if initial.Terminal() {
		return
	}

	heartbeat := time.NewTicker(heartbeatInterval)
	defer heartbeat.Stop()

	for {
		select {
		case <-c.Request.Context().Done():
			// Client disconnected (navigated away, closed the tab, network
			// drop) — nothing further to send; unsubscribe (deferred above)
			// releases this connection's Broadcaster channel.
			return
		case event, ok := <-ch:
			if !ok {
				return
			}
			if !writeEvent(c, event) {
				return
			}
			if event.Terminal() {
				return
			}
		case <-heartbeat.C:
			// A bare SSE comment line — never parsed as a data event by any
			// conforming client, purely a keep-alive signal.
			if _, err := c.Writer.Write([]byte(": heartbeat\n\n")); err != nil {
				return
			}
			c.Writer.Flush()
		}
	}
}

// writeEvent frames one VerificationEvent as `event: <type>\ndata:
// <json>\n\n` (the standard SSE wire format) and flushes it immediately —
// buffering would defeat a PROGRESS stream. Returns false on a write
// error (client gone), signaling the caller to stop.
func writeEvent(c *gin.Context, event VerificationEvent) bool {
	payload, err := json.Marshal(event)
	if err != nil {
		return false
	}
	if _, err := c.Writer.Write([]byte("event: " + event.Type + "\ndata: ")); err != nil {
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
