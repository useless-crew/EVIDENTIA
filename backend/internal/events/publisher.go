package events

import "context"

// Publisher is the ONE way any business service or background-job
// worker in this codebase publishes a real-time notification — never a
// scattered `redisClient.Publish(...)` call in a service method. Mirrors
// audit.Recorder.Record's own established contract deliberately: no
// error return, because a failed notification must NEVER fail (or roll
// back) the business operation that triggered it — Redis Pub/Sub is
// transport for a best-effort UI signal, not authoritative state (see
// this package's own doc comment), so a publish failure is logged
// operationally by the concrete implementation and otherwise silently
// absorbed, exactly like a Recorder.Record failure already is.
//
// eventType/resourceType/resourceID/data follow catalog.go's vocabulary —
// see that file for the full type/shape catalog. Implementations assign
// EventID/EventVersion/Timestamp themselves (see Event's own doc
// comment) — a caller never supplies or controls those.
type Publisher interface {
	Publish(ctx context.Context, eventType, resourceType, resourceID string, data any)
}

// NoopPublisher discards every event — a real, exported "events
// disabled" implementation (never a test-only stub in a _test.go file),
// useful wherever a Publisher is required but no real-time notification
// consumer exists yet for that call site's own tests, or a future
// deployment mode that runs without Redis event distribution at all.
type NoopPublisher struct{}

func (NoopPublisher) Publish(context.Context, string, string, string, any) {}
