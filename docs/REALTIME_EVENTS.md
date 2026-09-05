# Real-Time Events & Server-Sent Events

## Purpose

System 13 is Evidentia's ONE real-time notification architecture: how the
backend tells authorized, connected frontend clients that meaningful
state may have changed, without requiring continuous polling. It began
as System 11's audit-chain-verification-specific SSE implementation
(`internal/realtime`); System 13 generalizes that into reusable
infrastructure (`internal/events` + `internal/sse`) any future event
type builds on, and refactors System 11 onto it with no behavior change.
There is, and must remain, exactly one SSE implementation, one event
model, and one Redis-backed event-distribution mechanism in this
codebase.

```text
Business Operation (ShareService.CreateShare, DocumentService.
   |                 VerifyDocument/RedactDocument,
   |                 CertificateService.generateCertificate)
   |    or
   | Asynq Worker (AuditService.RunVerification)
   v
PostgreSQL Commit                     <- state change is durable FIRST
   |
Event Publisher (internal/events.Publisher.Publish)
   |
Redis (PUBLISH — internal/events.Channel, transport only)
   |
SSE Manager (internal/sse.Manager — one Redis SUBSCRIBE per backend
   |          process, fanned out in-process to locally-registered,
   |          already-authorized clients only)
   v
Authorized Frontend (EventStreamService -> AuditVerificationService /
                      CaseDetailComponent)
```

## Architectural Rule: PostgreSQL Remains Authoritative

An `Event` is a NOTIFICATION, never source-of-truth state. Redis Pub/Sub
is ephemeral: an event published while no backend process is subscribed,
or while a specific client is disconnected, is simply never seen by that
client — and this is an accepted, deliberate design choice, not a defect
to fix. **A missed event must never mean lost application state.** Every
consumer in this codebase follows the same pattern:

```text
REST  -> authoritative current state (always safe to (re)fetch)
SSE   -> notification that state MAY have changed (best-effort)
```

`AuditVerificationService.watch` (frontend) keeps a REST poll timer
running for the entire duration of a watched verification, independent of
whether SSE is connected — SSE only makes the UI feel faster; the poll
timer is what actually guarantees correctness. `CaseDetailComponent`
treats every case event, regardless of type, identically: a signal to
refetch `GET /cases/:id` — the event's own `data` is never rendered
directly as if it were the current state.

## Event Model

`internal/events.Event` (`backend/internal/events/event.go`) is the ONE
envelope every event uses:

```json
{
  "event_id": "...",
  "event_type": "AUDIT_VERIFICATION_PROGRESS",
  "event_version": 1,
  "timestamp": "2026-01-01T00:00:00Z",
  "resource_type": "audit_verification",
  "resource_id": "...",
  "data": { "...": "event-type-specific" }
}
```

- `event_id`: a fresh, random UUID per event — never client-supplied,
  never reused. Lets a frontend or log line correlate/deduplicate a
  specific event; also carried as the SSE frame's `id:` line for
  `Last-Event-ID` correlation (see "Reconnection" below).
- `event_type`: SCREAMING_SNAKE_CASE — the SAME naming convention this
  codebase's audit trail already established for `audit.Event.Action`
  (`DOCUMENT_UPLOADED`, `CASE_CREATED`, ...). Never a second convention
  mixed in (master prompt: "do not mix `audit.done` / `AUDIT_DONE` /
  `auditCompleted`").
- `event_version`: `CurrentEventVersion` (currently `1`) at publish
  time — lets a payload shape evolve later without silently breaking an
  older, still-connected frontend build.
- `resource_type`/`resource_id`: what the event is ABOUT. Together they
  form `ScopeKey` (`resource_type:resource_id`) — the exact authorization
  scope a client must already be registered for (see "SSE Manager &
  Authorization" below) before ever receiving it.
- `data`: event-type-specific, and never more than what's safe for
  whoever is authorized for that `resource_type`/`resource_id` — see
  "Event Catalog" below for each type's exact shape and what it
  deliberately omits.

Full catalog and Go/TS type definitions: `backend/internal/events/catalog.go`,
`frontend/src/app/core/models/api.models.ts`'s "System 13" section.

## Event Catalog

| `event_type` | `resource_type` | Published by | `data` |
|---|---|---|---|
| `AUDIT_VERIFICATION_STARTED` | `audit_verification` | `AuditService.RunVerification` | `AuditVerificationData` |
| `AUDIT_VERIFICATION_PROGRESS` | `audit_verification` | `AuditService.verifyBatches` (once per batch) | `AuditVerificationData` |
| `AUDIT_VERIFICATION_COMPLETED` | `audit_verification` | `AuditService.completeVerification` | `AuditVerificationData` |
| `AUDIT_INTEGRITY_FAILURE` | `audit_verification` | `AuditService.completeVerification` | `AuditVerificationData` |
| `AUDIT_VERIFICATION_FAILED` | `audit_verification` | `AuditService.MarkVerificationOperationallyFailed` | `AuditVerificationData` |
| `DOCUMENT_VERIFICATION_COMPLETED` | `case` | `DocumentService.VerifyDocument` | `DocumentVerificationData` |
| `CERTIFICATE_GENERATION_COMPLETED` | `case` | `CertificateService.generateCertificate` | `CertificateGenerationData` |
| `DOCUMENT_REDACTION_COMPLETED` | `case` | `DocumentService.RedactDocument` | `DocumentRedactionData` |
| `SHARE_CREATED` | `case` | `ShareService.CreateShare` | `ShareEventData` |
| `SHARE_REVOKED` | `case` | `ShareService.RevokeShare` | `ShareEventData` |

`DOCUMENT_VERIFICATION_FAILED`/`CERTIFICATE_GENERATION_FAILED`/
`DOCUMENT_REDACTION_FAILED` are defined constants (master prompt's own
requested catalog names them) but have **no current publisher** — see
`catalog.go`'s own doc comments for why: each corresponding operation's
actual failure path returns an HTTP error directly to the SAME request
that triggered it, which already tells that caller everything an
asynchronous notification would; there is no OTHER connected client who
benefits from being told about a request they never made. Never
published speculatively just to exercise the constant.

**Deliberately NOT implemented**: `DOCUMENT_PROCESSING_STARTED/PROGRESS/
COMPLETED/FAILED` — System 12 evaluated background document processing
and deliberately kept document hashing/certificate generation/redaction
synchronous (see `docs/BACKGROUND_JOBS.md`'s "Task Types"); there is no
background document-processing job in this codebase to emit these events
from, and inventing a fake one just to populate this catalog would
violate master prompt's own "do not create event types without an actual
consumer/use case." A generic `CASE_UPDATED`/`DOCUMENT_UPDATED` pair was
also not added on top of the five specific case-scoped events above — a
second, more generic layer over the same underlying facts would be
redundant, not additive.

## Event Scoping — Why `case`, Not `document`

`DOCUMENT_VERIFICATION_COMPLETED`, `CERTIFICATE_GENERATION_COMPLETED`,
`DOCUMENT_REDACTION_COMPLETED`, `SHARE_CREATED`, and `SHARE_REVOKED` are
all scoped to `case` (`ResourceTypeCase`), not to the individual
document, even though every one of them is fundamentally about ONE
document. This is a deliberate granularity choice: every one of these
operations already has the document's `case_id` on hand, a case-detail
view watching ONE stream (`GET /cases/:id/events`) is sufficient to
reactively refresh "this case's documents/shares changed" for every
document in it, and adding a SECOND, document-scoped endpoint
(`GET /documents/:id/events`) would double the new-endpoint surface for
no current consumer (master prompt: "only introduce ... where actually
useful"). If a future UI genuinely needs document-level granularity
(e.g. a single-document detail page that should NOT refresh on unrelated
case activity), add `GET /documents/:id/events` publishing the SAME event
types scoped to `ResourceTypeDocument` at that point — the infrastructure
(`internal/events`, `internal/sse`) needs no change to support it.

## SSE Manager & Authorization

`internal/sse.Manager` (`backend/internal/sse/manager.go`) is the ONE SSE
fan-out. It performs **no authorization of its own** — this is the single
most important thing to understand about it. A caller (an HTTP handler)
MUST have already verified, via the existing RBAC/ABAC/RLS machinery
(`authz.Service.CanAccessCase`/`CanAccessDocument`, or a service method
like `AuditService.GetVerification` that itself re-checks both), that the
connecting user is allowed to see events for the EXACT `resource_type`/
`resource_id` it is about to `Register` for. `Register` trusts its
`scopeKey` argument completely — this is safe only because every call
site is required to have proven it first:

- `GET /audit/verify-chain/:verificationId/events`
  (`internal/handlers/audit/events.go`) — re-runs
  `AuditService.GetVerification`'s RBAC (`audit:verify`, ADMIN-only) +
  `audit_verifications`' own RLS check.
- `GET /cases/:id/events` (`internal/handlers/case/events.go`) — sits
  behind `middleware.RequireCaseAccess(authz.ActionCaseRead, "id")`, the
  IDENTICAL ABAC check `GET /cases/:id` itself requires (case creator,
  active `case_members` row, or ADMIN) — a user with no relationship to
  the case is rejected with `403` before the connection is ever upgraded
  to a stream.

Neither route trusts the resource ID in the URL as proof of authorization
by itself (master prompt: "do not trust `verification_id`/case ID as
proof of authorization") — both re-run the SAME check every equivalent
REST endpoint already performs, every time a new connection opens
(including the periodic forced reconnect described below).

**Why one Redis subscription per process, not one per connection**:
`Manager.Start` opens exactly one `*redis.PubSub` against
`events.Channel` per backend process, receiving EVERY event published
anywhere; `dispatch` then routes each one, in Go, to only the
locally-registered channels whose `ScopeKey` matches. This is what lets
any number of connected clients share one Redis connection and one
JSON-decode per event, and is also what makes this design correct across
a FUTURE horizontally-scaled deployment (this project's docker-compose
runs one backend instance today, but the design does not assume that
will always be true): every replica's own `Manager` receives every event
and independently delivers it to only its own locally-connected,
already-authorized clients.

## Redis's Role

Redis is transport ONLY — `events.Channel` (`evidentia:events`), a single
Pub/Sub channel every `RedisPublisher.Publish` call PUBLISHes to and
every `Manager.Start` SUBSCRIBEs to. It holds no durable event history,
no business state, and is never the source of truth for anything this
system reports — PostgreSQL (`audit_verifications`, `documents`,
`document_shares`, `compliance_certificates`) remains authoritative for
every fact an event's `data` describes. This is the SAME shared
`*redis.Client` (`internal/cache.Cache.Client()`) every other Redis-backed
component in this codebase already uses — never a second connection pool,
and never the same Redis usage as System 12's Asynq task queue (Asynq is
for job EXECUTION; this Pub/Sub channel is for event DISTRIBUTION — two
independent uses of the one Redis instance, never conflated).

**If Redis becomes unavailable**: `RedisPublisher.Publish` logs the
failure and returns (never propagates an error to the business operation
that triggered it — see `events.Publisher`'s own doc comment, mirroring
`audit.Recorder.Record`'s established no-error contract exactly). REST
APIs continue functioning normally; only SSE notifications are affected
(existing connections may stop receiving new events; `Manager.Start`'s
Redis subscription may itself disconnect). Every frontend consumer
already falls back to REST on any stream interruption (see
"Delivery Semantics" below) — Redis is explicitly never a single point
of failure for core persistent application state.

## Delivery Semantics

**At-most-once, best-effort.** This system asserts nothing stronger.
Redis Pub/Sub delivers to whichever processes are subscribed at the
moment of `PUBLISH`; a process that isn't currently subscribed (starting
up, momentarily disconnected) never sees that message, and there is no
retry or backlog. Within ONE resource's own event stream, ordering is
preserved in practice — every event for a given verification/case is
published by the single goroutine/request that owns that operation at
that moment, and Redis Pub/Sub delivers messages to each subscriber in
the order the server received the `PUBLISH` calls — but this system makes
no CROSS-resource ordering guarantee, and does not need one: nothing
compares events from two different resources against each other.

## Reconnection & `Last-Event-ID`

Every SSE frame carries an `id:` line (`event.event_id`) per the SSE
spec's `Last-Event-ID` convention — but this system does **not**
implement replay against it: there is no durable event history to replay
from (see "Redis's Role" above), so a reconnecting client's browser-level
`Last-Event-ID` header, if sent, is simply ignored server-side. `id:` is
carried purely for CORRELATION/deduplication (a frontend that happens to
receive the same `event_id` twice — e.g., a reconnect racing an
in-flight duplicate — can safely ignore the repeat), never for gap
recovery. A client that needs to know it missed something falls back to
REST — see "Architectural Rule" above.

`internal/sse.Stream`'s `maxConnectionDuration` (1 hour) forces every SSE
connection to end and reconnect periodically, EVEN if nothing else would
have closed it — this is what keeps a long-lived, otherwise-endless
resource stream (`GET /cases/:id/events` has no terminal event) from ever
growing authorization-stale: a user whose access to a case is revoked
mid-connection is guaranteed to be re-checked (`RequireCaseAccess` runs
again on the forced reconnect) within that bound, never indefinitely
subscribed on stale authorization.

## Heartbeat

A 15-second SSE comment line (`: heartbeat\n\n`) keeps intermediate
proxies/load balancers from timing out an idle connection between real
events, and lets the server notice a dead client connection promptly.
Never itself an `events.Event`, never audited, never published to Redis —
purely a transport-layer keep-alive (`internal/sse/stream.go`).

## Connection Management, Limits & Backpressure

- **Per-user connection limit**: `internal/sse.Manager` caps each
  authenticated user at `maxConnectionsPerUser` (10) concurrent
  registrations, returning `ErrTooManyConnections` (translated to HTTP
  `429`) beyond that — no dedicated rate-limiting middleware exists
  anywhere in this codebase to reuse (see `docs/BACKGROUND_JOBS.md`'s own
  "Rate Limiting" finding for System 12's identical situation), so this
  is a small, self-contained, SSE-specific counter, never a
  general-purpose HTTP rate limiter.
- **Bounded per-connection buffer**: each registered channel holds at
  most `subscriberBufferSize` (8) undelivered events; `dispatch` never
  blocks on a slow consumer — a full buffer means the newest event for
  that connection is simply dropped (never an unbounded queue). A
  dropped PROGRESS event is superseded by the next one anyway; a dropped
  TERMINAL event is still recoverable through the connection's own REST
  poll backstop (audit) or the next case event / a manual refresh (case
  events).
- **Cleanup**: `Register`'s returned `unsubscribe` function (always
  called via `defer` from `Stream`) removes the connection's channel from
  `Manager`'s internal map and decrements its owner's connection count —
  never leaves a disconnected client registered. The channel itself is
  deliberately never `close()`d (see `Register`'s own doc comment) to
  avoid a send-on-closed-channel panic racing a concurrent `dispatch`
  call — an unreferenced channel is ordinary, garbage-collectable Go.
- **Graceful shutdown**: `cmd/server/main.go`'s `run()` launches
  `SSEManager.Start` in its own goroutine bound to a dedicated,
  explicitly-cancelled context (`sseCtx`) — cancelled unconditionally
  during shutdown (not relying on the top-level signal context alone,
  which is only cancelled by the SIGINT/SIGTERM branch, never the
  server-error/worker-error branches) — and waits (bounded by the same
  shutdown timeout every other step respects) for `SSEManager.Done()`
  before closing the shared Redis client out from under it.

## System 11 Integration

`internal/handlers/audit/events.go` and `internal/service/audit_service.go`
are fully refactored onto this infrastructure — see each file's own doc
comments. Behavior is UNCHANGED from System 11: `POST /audit/verify-chain`
→ `202` → `GET /audit/verify-chain/:id/events` streams
`AUDIT_VERIFICATION_STARTED` → `AUDIT_VERIFICATION_PROGRESS` (repeated) →
`AUDIT_VERIFICATION_COMPLETED`/`AUDIT_INTEGRITY_FAILURE`/
`AUDIT_VERIFICATION_FAILED`, verified end-to-end (real Docker deployment):
`VERIFIED` before tampering, `AUDIT_INTEGRITY_FAILURE` (with safe
`failed_entry_id`/`failure_type`/`failure_reason`) after tampering a TEST
audit entry, `VERIFIED` again after restoring it. The one race this
refactor had to re-prove (and did, via
`internal/httpserver/audit_flow_integration_test.go`'s `TestAuditFlow_SSE`):
`Events` registers with the Manager BEFORE re-running its authorization
check, so a fast verification's one completion event, published in the
gap between registration and the check, is still captured rather than
missed (see that handler's own doc comment).

## System 12 Integration

`AuditService.RunVerification` — running inside System 12's embedded
Asynq worker — publishes every `AUDIT_VERIFICATION_*`/`AUDIT_INTEGRITY_
FAILURE` event through the SAME `events.Publisher` any synchronous
service method uses; there is no separate "worker event path". Asynq
itself is never used as an event bus (master prompt: "Asynq is for job
execution; Redis/event infrastructure is for real-time event delivery")
— the worker's only interaction with this system is calling
`Publisher.Publish` after each PostgreSQL write, exactly like
`AuditService`'s own synchronous methods do.

## Security & Privacy

Every event's `data` shape (`catalog.go`) was reviewed field-by-field for
what it must NOT contain: no raw document contents, no witness/redaction-
sensitive metadata (System 8's own privacy model), no recipient identity
or permission level on a share event (a case member merely watching for
"the share list changed" does not need to already know who or how — see
`ShareEventData`'s own doc comment), no credentials, no JWTs, no MinIO/
database internals, no SQL text, no stack traces. `LoggingMiddleware`-
style operational logging in this package (`RedisPublisher`'s own
`slog` calls) logs only `event_type`/`event_id`/`resource_type` — never
`data`'s contents.

Explicit answers to this system's own required security review:

1. **Unauthenticated SSE connection?** Rejected `401` — both routes
   require the same `authMW` every other route does.
2. **Authenticated but unauthorized user receiving another case's/
   verification's events?** No — `RequireCaseAccess`/
   `AuditService.GetVerification` reject with `403`/`404` before any
   data is ever written to the connection; verified live (an unrelated
   POLICE officer gets `403` opening a case's stream) and by
   `TestCaseEvents_SSE_DeliversShareCreatedAndEnforcesIsolation`.
3. **One agency/case receiving another's events?** No —
   `Manager.dispatch` only ever sends to channels registered under the
   EXACT matching `ScopeKey`; verified by
   `TestManager_EventsAreScopedToTheirOwnResource` (unit) and
   `TestCaseEvents_SSE_DeliversShareCreatedAndEnforcesIsolation`'s own
   cross-case check (an event published for case B never reaches case
   A's stream).
4. **Client subscribing to arbitrary resources?** No — `Register` is
   never called with a client-supplied scope directly; the URL path
   parameter is parsed, then authorized via the existing RBAC/ABAC/RLS
   machinery, and only THEN used to build the scope key.
5. **Query parameters bypassing authorization?** No mechanism in this
   system reads an authorization-relevant value from a query string at
   all — both routes derive their entire scope from the URL PATH
   parameter, itself independently authorized.
6. **Event payloads leaking sensitive document data?** No — see the
   catalog review above.
7. **JWTs in URLs?** Never — both the audit and case SSE clients use
   `fetch()` with a normal `Authorization: Bearer` header (see
   `EventStreamService`'s own doc comment for why not `EventSource`,
   which cannot set headers at all).
8. **Redis accessed directly from the frontend?** No — the frontend only
   ever talks to the HTTPS API and the authenticated SSE endpoints; Redis
   is never exposed, and its address/credentials are backend-only
   configuration.
9. **One client exhausting server memory?** Bounded by
   `maxConnectionsPerUser` and `subscriberBufferSize` (see "Connection
   Management" above).
10. **A slow client blocking others?** No — `dispatch` never blocks;
    see "Connection Management" above and
    `TestManager_SlowSubscriberDropsRatherThanBlocks`.
11. **Redis failure corrupting PostgreSQL state?** No — Redis carries no
    business state; see "Redis's Role" above.
12. **An SSE event treated as authoritative business state?** No — see
    "Architectural Rule" above; every frontend consumer refetches via
    REST rather than rendering event `data` as current state.
13. **Reconnect creating duplicate UI effects?** Each event carries a
    unique `event_id` for a consumer that wants to deduplicate; more
    fundamentally, every consumer in this codebase reacts to an event by
    triggering an idempotent REFETCH (never an incremental mutation), so
    a duplicate delivery just refetches twice — never double-applies a
    change.
14. **Verification events recursively creating audit records?**
    No — unchanged from System 11: `AuditService` records exactly two
    audit events per verification run regardless of how many progress
    events it publishes (see `docs/AUDIT_CHAIN.md`'s "Avoiding recursive
    audit-access events").
15. **Workers bypassing RLS?** No — `AuditService.RunVerification`'s own
    `workerIdentity` mechanism (unchanged from Systems 11/12) is what
    established the RLS context for the PostgreSQL writes that PRECEDE
    each publish call; this package's event publishing itself touches no
    database at all.
16. **Disconnected clients remaining registered forever?** No — see
    "Connection Management" above (`unsubscribe`, always deferred).

## Frontend Integration

`EventStreamService` (`frontend/src/app/core/services/event-stream.service.ts`)
is the one central SSE client — every component/service that needs a
live stream calls `connect(path, onEvent, onConnectionChange)` and gets
back a `stop()` function, never a bespoke `fetch()`/`ReadableStream`
parsing loop of its own. Two current consumers:

- `AuditVerificationService` (refactored from its own hand-rolled
  connection logic onto this shared service) — unwraps each
  `RealtimeEvent<AuditVerificationEventData>`'s `data` into the `current`
  signal the audit-integrity dashboard already reads.
- `CaseDetailComponent` — opens `GET /cases/:id/events` on load, and on
  ANY event (regardless of type — `DOCUMENT_VERIFICATION_COMPLETED`,
  `CERTIFICATE_GENERATION_COMPLETED`, `DOCUMENT_REDACTION_COMPLETED`,
  `SHARE_CREATED`, `SHARE_REVOKED`) simply refetches `GET /cases/:id` —
  the TanStack-Query-equivalent "invalidate and refetch" pattern this
  Angular codebase already established elsewhere (there is no TanStack
  Query in this project; the equivalent here is a plain service method
  re-call, matching `AuditLogComponent.onVerificationSettled`'s own
  existing convention). A small, subtle "Live" indicator shows when the
  stream is connected; its absence never blocks the page — every field
  it displays comes from the REST-fetched `detail` signal regardless
  (see "Offline / Disconnected State" below).

Neither consumer creates a generic "job manager"/"event feed" UI — each
integrates directly into the existing, domain-specific screen it belongs
to (master prompt: "the frontend should interact with domain-specific
operations").

## Offline / Disconnected State

If an SSE connection drops (network blip, backend restart, Redis
outage), `EventStreamService` reports `onConnectionChange(false)` and
stops — it does not loop reconnecting itself. `AuditVerificationService`'s
REST poll timer keeps running regardless, so the audit dashboard
continues updating (a little less "live", never stuck). `CaseDetailComponent`
simply stops receiving push notifications until the user next navigates
to (or revisits) the case, at which point `ngOnInit` reconnects and
`fetch()` already re-runs — the page is never unusable, and no error
banner is shown for a dropped notification stream (only the small "Live"
indicator disappears).

## Testing

- `internal/events` — envelope construction (`buildEvent`), unique event
  IDs, `ScopeKey` collision-freedom, `NoopPublisher`.
- `internal/sse` — `manager_test.go` (fan-out, cross-resource isolation,
  unsubscribe, slow-consumer backpressure bound, per-user connection
  limit) with no real Redis (direct `dispatch` calls); `manager_
  integration_test.go` (`-tags=integration`, real Redis) proving the
  actual `RedisPublisher` → `Manager.Start` → registered-channel round
  trip, and that an unrelated scope never receives a delivery.
- `internal/httpserver` — `TestAuditFlow_SSE` (System 11 regression,
  updated for the new event-type constants and `job_id`, unchanged
  behavior otherwise) and `TestCaseEvents_SSE_DeliversShareCreatedAndEnforcesIsolation`
  (System 13's own new end-to-end proof: a real `SHARE_CREATED` event
  delivered over a real SSE connection, an outsider rejected with `403`,
  and cross-case isolation — an event published for an unrelated case
  never reaching this case's stream).

Run: `go test ./...`, `go test -race ./...`,
`go test -tags=integration -p 1 ./...` (requires the docker-compose
postgres/redis stack up and migrated).

## Limitations & Follow-Up

- No durable event history/replay exists — see "Reconnection" above;
  this is a deliberate scope decision (master prompt: "do not
  automatically introduce Redis Streams unless there is a concrete
  requirement" / "do not add an outbox simply for complexity"), not an
  oversight. Every event this system publishes today is a best-effort UI
  notification for a fact PostgreSQL already durably recorded through an
  existing, independent path (the audit trail, `audit_verifications`,
  `document_shares`, ...) — nothing depends on Redis for durability.
- Document/share events are scoped to `case`, not the individual
  document — see "Event Scoping" above for why, and how to extend this if
  a future UI genuinely needs finer granularity.
- No transactional outbox: each publish call happens as a plain
  post-commit step (see each service's own call site) — inspected and
  judged unnecessary for ordinary UI notifications where PostgreSQL,
  never Redis, is authoritative (see "Database Transaction + Event
  Publication" in this document's own architecture section above). A
  future event type with a genuine guaranteed-delivery requirement should
  reconsider this, following the outbox pattern master prompt describes,
  rather than assuming this system's existing best-effort posture
  automatically extends to it.
