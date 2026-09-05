// Package events is System 13's ONE real-time event model and publisher
// abstraction — how a business operation or background worker notifies
// authorized frontend clients that meaningful state may have changed,
// without becoming a second source of truth. PostgreSQL remains
// authoritative for every fact an Event ever describes; an Event is a
// best-effort NOTIFICATION that something changed, never the change
// itself — a client that misses one always recovers current state
// through the existing REST API (see internal/sse for the delivery side
// of this: authorized SSE connections that receive Events published
// here).
//
// This package depends on nothing application-specific (no PostgreSQL,
// no authz, no HTTP) beyond Redis and the standard library — a business
// service (internal/service) depends on Publisher to publish; a
// transport-layer manager (internal/sse) depends on this package's Event
// type to receive and route what was published. Neither direction
// creates an import cycle back into internal/service.
package events

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// CurrentEventVersion is embedded in every Event as event_version — see
// that field's own doc comment for why (schema evolution without
// breaking an older frontend build).
const CurrentEventVersion = 1

// Channel is the ONE Redis Pub/Sub channel every Event is published to
// and every internal/sse.Manager subscribes to, regardless of event type
// or resource — see internal/sse's own package doc comment for why a
// single channel, with server-side Go routing/filtering by ScopeKey, is
// the correct design here (never a channel-per-resource, which would
// mean a Redis SUBSCRIBE per SSE connection — expensive and unnecessary
// when one subscription per backend PROCESS, fanned out in-process to
// every locally-connected client, scales the same way regardless of how
// many resources exist).
const Channel = "evidentia:events"

// Event is Evidentia's one real-time notification envelope — every event
// this system ever publishes or delivers has exactly this shape, encoded
// as this Go type (Redis message payload, SSE `data:` line, and the JSON
// a connected client parses).
//
//   - EventID: a fresh, random UUID assigned by Publish for every
//     event — never client-supplied, never reused — so a frontend or log
//     line can correlate/deduplicate a specific event (see this type's
//     own "Delivery Semantics" note below).
//   - EventType: one of the Type* constants in catalog.go — always
//     SCREAMING_SNAKE_CASE, the SAME naming convention this codebase's
//     audit trail already established for audit.Event.Action
//     (DOCUMENT_UPLOADED, CASE_CREATED, ...). Never a second convention
//     (audit.done / auditCompleted) mixed in.
//   - EventVersion: CurrentEventVersion at publish time — lets a payload
//     shape evolve later without silently breaking an older, still-
//     connected frontend build; a consumer that does not recognize a
//     version (or event type) must ignore the event safely, never crash.
//   - Timestamp: UTC, set by Publish — never client-supplied.
//   - ResourceType/ResourceID: what this event is ABOUT — e.g.
//     ("audit_verification", "<verification-id>") or ("case", "<case-id>").
//     Together they form ScopeKey, the exact authorization scope a
//     client must already be registered for (internal/sse.Manager.
//     Register) before ever receiving this event — see that package's
//     own doc comment for the full authorization model. ResourceID is a
//     string, not a typed ID, because not every resource type's ID is a
//     UUID and this package must not need to know.
//   - Data: event-type-specific payload, already validated/shaped by the
//     PUBLISHING call site to contain only fields safe to expose to
//     whoever is authorized for ResourceType/ResourceID — see catalog.go
//     for each event type's own Data shape and what it deliberately
//     omits (raw document contents, credentials, internal-only IDs,
//     witness/privacy-sensitive fields a recipient may not be cleared to
//     see).
//
// Delivery semantics: at-most-once, best-effort. Redis Pub/Sub is
// ephemeral — an event published while no backend process is subscribed,
// or while a specific client is disconnected/reconnecting, is simply
// never seen by that client. This is an explicit, accepted design choice
// (see this package's own doc comment): PostgreSQL, never this event
// stream, is authoritative, so a missed event never means lost
// application state — the frontend re-fetches current state through
// the existing REST endpoints. This package makes no stronger delivery
// guarantee than that, and must never be described as exactly-once or
// durable.
type Event struct {
	EventID      uuid.UUID       `json:"event_id"`
	EventType    string          `json:"event_type"`
	EventVersion int             `json:"event_version"`
	Timestamp    time.Time       `json:"timestamp"`
	ResourceType string          `json:"resource_type"`
	ResourceID   string          `json:"resource_id"`
	Data         json.RawMessage `json:"data"`
}

// ScopeKey is the routing/authorization key this Event is delivered
// under — see internal/sse.Manager.Register/dispatch. Two events with
// the same ResourceType/ResourceID always share a ScopeKey (and are
// delivered to the same set of registered clients); two different
// resources never collide (a colon can never appear inside ResourceType
// itself — every ResourceType this package defines is a fixed, hand-
// written constant, never client- or otherwise dynamically-supplied
// text that could smuggle an extra colon into the key).
func (e Event) ScopeKey() string { return ScopeKey(e.ResourceType, e.ResourceID) }

// ScopeKey builds the same routing/authorization key Event.ScopeKey
// derives from an already-built Event — exported so a caller authorizing
// a subscription (e.g. an HTTP handler about to call
// internal/sse.Manager.Register) can compute the SAME key from a
// resource type/ID pair it already has, without needing to construct a
// throwaway Event first.
func ScopeKey(resourceType, resourceID string) string {
	return resourceType + ":" + resourceID
}
