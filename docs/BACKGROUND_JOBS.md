# Background Jobs

## Purpose

System 12 is Evidentia's ONE reusable background-job architecture:
Redis + Asynq + Go workers, used to move genuinely expensive or
long-running operations out of normal HTTP request/response execution.
It did not begin as a green-field build — System 11 (audit-chain
verification) already introduced Asynq to this codebase; System 12
generalizes what System 11 built (queue priority, error classification,
structured logging, job traceability) into infrastructure any future task
type reuses, and refactors System 11's own task onto it. There is, and
must remain, exactly one `*asynq.Server`/`*asynq.Client` pair in this
codebase (`internal/jobs`) — never a second queue system.

```text
Frontend
   |
POST /audit/verify-chain            (or another domain-specific route —
   |                                  see "API Design" below; never a
   v                                  generic POST /jobs/execute)
Handler (internal/handlers/audit)
   |
Service (internal/service.AuditService.StartVerification)
   |                                  <- authorization happens HERE,
   |                                     before anything is enqueued
   | INSERT audit_verifications (QUEUED)
   v
jobs.Client.EnqueueVerifyAuditChain
   |
Redis (Asynq queue — transport only, never authoritative business state)
   |
asynq.Server + ServeMux              (internal/jobs — embedded in
   |                                  cmd/server, not a separate binary)
   | LoggingMiddleware                (start/duration/attempt/failure —
   v                                   every task type, uniformly)
Handler.ProcessTask                  (internal/jobs/audit_verification.go)
   |
Service (internal/service.AuditService.RunVerification)
   |                                  <- re-establishes RLS transaction
   |                                     context itself; the worker has
   |                                     no HTTP request to inherit one
   |                                     from
   v
PostgreSQL (batched reads/writes — authoritative business state)
   |
Job Result / Status (audit_verifications row)
   |
API (GET /audit/verify-chain/:id) / SSE (GET .../events)
```

## Existing Asynq Infrastructure (System 11) — What Was Reused

Every piece of System 11's Asynq integration was kept and generalized,
never replaced:

- `internal/jobs.Client` (enqueueing) — unchanged in shape; still the
  ONE way any service enqueues a background task.
- `internal/jobs.NewServer`/`NewMux` (`queue.go`) — extended with named,
  weighted queue priorities and a shared logging middleware (see below),
  not rewritten.
- `internal/jobs.AuditVerificationHandler`/`NewAuditVerificationErrorHandler`
  (`audit_verification.go`) — the exact same retry/error-handling design
  (an operational failure is retried per `asynq.MaxRetry`/`asynq.Timeout`;
  a cryptographic `INTEGRITY_FAILURE` is a nil-error, successfully
  COMPLETED task, never retried), now built on this package's shared
  `FailureCategory`/`Permanent`/`CategoryOf` vocabulary instead of an ad
  hoc `asynq.SkipRetry` wrap.
- SSE (`internal/realtime` at the time System 12 was written, later
  generalized into `internal/events`/`internal/sse` by System 13 — see
  docs/REALTIME_EVENTS.md) — untouched BY SYSTEM 12: it introduces no
  second real-time mechanism; audit verification's progress dashboard
  streams through whichever SSE infrastructure is current, unchanged in
  behavior across that later refactor.
- `audit_verifications` (migration `000005`) — completely untouched.
  System 12 introduces NO new persistent job table: this table is
  intentionally domain-specific (it IS the audit-chain verification
  record, an evidentiary artifact, not a generic job queue), so it stays
  domain-specific — see "Job Status & Persistence" below for why a
  generic job table was deliberately NOT introduced alongside it.

## Task Types

**Implemented: `AUDIT_CHAIN_VERIFY`** (`audit:verify_chain` —
`TypeVerifyAuditChain` in `internal/jobs/audit_verification.go`), unchanged
in behavior from System 11, now running on `QueueCritical` (see "Queue
Strategy" below) with a deterministic, traceable job ID.

**Deliberately NOT implemented**, after inspecting Systems 1-11 for
genuinely expensive candidates:

- Document upload hashing (System 6) — already synchronous, already
  correct (SHA-256 is computed via `io.TeeReader` while streaming to
  MinIO, before the document row is ever committed). Master prompt's own
  "Document Hashing" section requires this to stay synchronous; moving
  it asynchronous would mean a document could be "committed" before its
  canonical hash is known, which this project's integrity model does not
  permit.
- Compliance-certificate generation (System 7) — its one expensive step
  (recomputing the document's SHA-256 from its stored MinIO object) is
  already bounded by `DocumentsConfig.MaxUploadSize` and completes well
  within an ordinary HTTP request. Moving it asynchronous would also
  complicate, for no benefit, the property master prompt is most
  insistent on here: "the certificate MUST NOT be generated for an
  unverified document" — the current design enforces this by construction
  (hash re-verification and signing happen in the same call), which an
  asynchronous version would have to re-derive across a queue boundary.
- Document redaction (System 8) — a single-image, in-memory decode/
  redact/re-encode operation bounded by the same upload size limit; a
  sub-second CPU operation for any file size this system accepts, not a
  long-running one. Moving it asynchronous would force its current
  simple "the derivative is returned in this response" contract into an
  asynchronous poll/SSE pattern for no corresponding benefit.
- `DOCUMENT_THUMBNAIL_GENERATION`/`DOCUMENT_METADATA_EXTRACTION` — no
  system through 11 implements thumbnailing or metadata/OCR extraction at
  all; inventing a background task for a feature that does not otherwise
  exist would be scope creep, not infrastructure.

See `backend/internal/jobs/certificate_generation.go` and
`document_processing.go` (kept as placeholder files, not deleted, exactly
per this project's existing "reserved for a future system" convention)
for this same reasoning inline with the code.

A future task type that genuinely needs this infrastructure (e.g. a
future PDF-rendering/watermarking certificate format, if one is ever
added) follows `AUDIT_CHAIN_VERIFY`'s own pattern: a `Type*` constant, a
payload carrying only trusted server-generated IDs, a `Handler`
implementing `asynq.Handler`, registered on the same `NewMux` — never a
second `asynq.Server`/`asynq.Client`.

## Job Definitions, Dispatch, Handlers — Separation of Concerns

- **Job definitions** (`internal/jobs/audit_verification.go`'s
  `VerifyAuditChainPayload`, `NewVerifyAuditChainTask`): the task type
  constant, its (minimal, trusted-ID-only) payload shape, and its Asynq
  options (queue, retry, timeout, deterministic ID).
- **Job dispatch** (`internal/jobs.Client`): the one `Enqueue`-wrapping
  type every service calls through — never a raw `*asynq.Client` used
  directly by a service.
- **Job handlers** (`AuditVerificationHandler`): adapts a narrow,
  package-local interface (`AuditVerifier`) to `asynq.Handler` — the
  handler itself contains no PostgreSQL/business logic; see "RLS in
  Workers" below for where that actually lives.
- **Job processing** (`internal/service.AuditService.RunVerification`):
  the actual business logic — reusing System 10's verifier unchanged
  (see `docs/AUDIT_CHAIN.md`).
- **Job status**: for `AUDIT_CHAIN_VERIFY`, the `audit_verifications` row
  itself (System 11) — see "Job Status & Persistence" below.
- **Job retry policy**: `internal/jobs/errors.go`'s `FailureCategory`
  vocabulary plus each task type's own `asynq.MaxRetry`/`asynq.Timeout` —
  see "Retry Policy" below.

## Queue Strategy

Two named, weighted queues (`internal/jobs/queue.go`):

| Queue | Weight | Carries |
|---|---|---|
| `critical` | 6 | Security-critical work — `AUDIT_CHAIN_VERIFY` today. |
| `default` | 2 | Reserved for a future, non-security-critical task type. |

Asynq's weighted scheduler pulls from a higher-weighted queue more often
(never a hard starvation guarantee on its own, but the standard Asynq
idiom — see [Queue Priority](https://github.com/hibiken/asynq/wiki/Queue-Priority)).
A third `document` queue was deliberately NOT added: master prompt's own
"only introduce multiple queues if actually useful" / "do not create
unnecessary queue complexity" — an empty queue name a weighted scheduler
still has to consider, for a task type that does not exist, is exactly
the complexity to avoid. Add one only when a real task type needs it.

`serverConcurrency = 5` bounds how many tasks this ONE worker pool runs
at once, globally, across every queue — never per-task-type unlimited
concurrency (master prompt: "a malicious or accidental job [must not]
exhaust resources").

## Worker Lifecycle & Graceful Shutdown

The worker (`asynq.Server` + `asynq.ServeMux`) runs embedded in the same
process/binary as the HTTP server (`cmd/server/main.go`) — there is no
separate `worker` container in `docker-compose.yml`, and this workload
does not justify one (see that file's own doc comment for the full
reasoning, unchanged from System 11).

On `SIGINT`/`SIGTERM` (`cmd/server/main.go`'s `run`):

1. The HTTP server stops accepting new connections
   (`server.Shutdown(shutdownCtx)`, bounded by `Server.ShutdownTimeout`).
2. `worker.Shutdown()` stops the Asynq server from pulling NEW tasks and
   waits for any task **already in flight** to finish its current
   `ProcessTask` call before returning — a verification already `RUNNING`
   is allowed to reach its own next batch checkpoint rather than being
   killed mid-batch, so it never leaves `audit_verifications` in a state
   that is neither a properly-progressed row nor a properly-failed one.
3. `a.Close()` releases every remaining resource (database pool, Redis,
   MinIO client) exactly once, in the deferred order `internal/app.App`
   already established.

Docker Compose's own `stop` sends `SIGTERM` and waits (default grace
period) before `SIGKILL` — this shutdown sequence is designed to complete
well within that window for the workloads this system runs today.

## Retry Policy

`internal/jobs/errors.go` defines the classification vocabulary master
prompt's "Retry Policy" section asks for:

| Category | Meaning | Retried? |
|---|---|---|
| `TRANSIENT` | A momentary PostgreSQL/Redis/object-storage blip — the DEFAULT for any plain, unclassified error a `Handler.ProcessTask` returns. | Yes, per the task's own `asynq.MaxRetry`/backoff. |
| `PERMANENT` | Retrying can never fix it (a malformed task payload, a resource that no longer exists). | No — `Permanent(...)` combines the category with `asynq.SkipRetry`. |
| `SECURITY` | A permanent failure specifically because the operation was never authorized. Reserved for a future task type whose worker itself detects a post-enqueue authorization problem; no task type needs it today. | No. |
| `INTEGRITY` | A cryptographic/structural finding (System 11's `INTEGRITY_FAILURE`). **Never reaches this classification as an error at all** — `RunVerification` returns `nil` for both `VERIFIED` and `INTEGRITY_FAILURE`, exactly like System 10's synchronous verifier treated both as a `200`. Listed here only so the complete vocabulary lives in one place; never conflated with `PERMANENT` (an outage is not evidence of tampering) or retried (a tamper finding is not a transient error). | N/A — not an error return. |

`asynq.MaxRetry`/`asynq.Timeout` (configured per task type —
`verifyAuditChainMaxRetry = 3`, `verifyAuditChainTimeout = 30m` for
`AUDIT_CHAIN_VERIFY`, unchanged from System 11) provide Asynq's own
exponential backoff between attempts — this package invents no separate
backoff mechanism (master prompt: "use exponential/backoff retry behavior
through Asynq or the existing project configuration").

A task row is only marked terminally `FAILED` once Asynq's retry budget
is **exhausted** (`isRetriesExhausted`, called from
`NewAuditVerificationErrorHandler`) — never on an intermediate attempt,
so a transient blip that succeeds on retry 2 never leaves a stray
`FAILED` row alongside the eventual real result.

## Idempotency

Two independent layers, neither a substitute for the other:

1. **Database-level**: `idx_audit_verifications_single_active` (a unique
   index on a constant expression filtered to `status IN ('QUEUED',
   'RUNNING')`) guarantees at most one verification is ever active,
   regardless of how many times `POST /audit/verify-chain` is called
   concurrently — unchanged from System 11.
2. **Asynq-level (System 12, new)**: `NewVerifyAuditChainTask` passes
   `asynq.TaskID(AuditVerifyChainJobID(verificationID))` — a deterministic
   ID derived from the verification's own UUID (`internal/jobs/ids.go`).
   Enqueueing the SAME verification ID twice is rejected by Asynq itself
   (`asynq.ErrTaskIDConflict`) rather than silently creating a second
   task. This also gives every task type built on this infrastructure a
   free, traceable `job_id` to return to API callers (see "Job IDs"
   below) — the two benefits share one mechanism, not two.

Every task type's own worker-side logic must independently tolerate being
run more than once (master prompt: "a worker can execute a task more than
once"): `RunVerification`'s `MarkAuditVerificationRunning`'s
`WHERE status = 'QUEUED'` guard already makes a redelivered/duplicate
attempt a safe no-op (see `docs/AUDIT_CHAIN.md`'s "Concurrency &
idempotency" for the full detail, unchanged by System 12).

## Job IDs

`internal/jobs/ids.go`'s `DeterministicTaskID(taskType, entityID)` is the
one, reusable mechanism every task type uses to derive its own traceable
`job_id` — never a randomly generated one, and never persisted as a
separate database column (it is always re-derivable from the entity ID
alone, so there is nothing to keep in sync). `POST /audit/verify-chain`
and `GET /audit/verify-chain/:id` both return it:

```json
{ "verification_id": "...", "job_id": "audit:verify_chain:...", "status": "QUEUED", "created_at": "..." }
```

## Job Status & Persistence

Master prompt's generic job-status model (`id`, `task_type`, `status`,
`requested_by_user_id`, `progress`, timestamps, `attempts`, `error
category`, `metadata`) is deliberately **not** introduced as a new,
separate table: `audit_verifications` (System 11) already IS exactly this
model, specialized for its one domain — and per master prompt's own
guidance ("if System 11's verification table is intentionally
domain-specific: keep it domain-specific and build reusable job
infrastructure around it"), that is exactly what System 12 does. A
second, generic job table would either duplicate `audit_verifications`
for no reason, or need to somehow unify with it — both worse than the
current design for a codebase with exactly one task type. A future task
type with a genuinely different persistence need gets its own
domain-specific table (following `audit_verifications`' own pattern —
migration, RLS, grants), not a shared generic one.

Master prompt's generic `QUEUED`/`RUNNING`/`COMPLETED`/`FAILED` lifecycle
is the CONCEPTUAL model this package's infrastructure is built around;
`AUDIT_CHAIN_VERIFY` itself preserves System 11's own, more specific
terminal vocabulary (`VERIFIED`/`INTEGRITY_FAILURE` in place of a generic
`COMPLETED`) — master prompt is explicit that a security-specific result
must never be replaced with a generic one, and System 12 does not do so.

## RLS in Workers

The worker has no HTTP request to inherit an RLS transaction context
from — it must establish one explicitly, every time, for every query it
runs. `internal/service.AuditService`'s worker-side methods
(`RunVerification`, `MarkVerificationOperationallyFailed`, and
`reconcileStale`'s own queries) all run under `workerIdentity` — a fixed,
documented `repository.AppIdentity{UserID: workerIdentityUserID, Role:
models.RoleAdmin}` sentinel (see that variable's own doc comment in
`audit_service.go`) — passed to `repository.WithTx` exactly like every
HTTP-request-scoped call passes the REAL caller's identity. This is the
SAME mechanism (`SET LOCAL app.user_id`/`app.role` inside the transaction
— see `docs/DATABASE_SCHEMA.md`), never a different, RLS-bypassing code
path. `evidentia_app` (the runtime role the worker's connection pool uses
— the SAME pool the HTTP server uses, never a second one) holds no
`BYPASSRLS`, exactly like every other System 1-11 database access.

`workerIdentity` uses `ADMIN` because every RLS policy on
`audit_verifications`/the chain-verification read path this worker
exercises checks `current_app_role() = 'ADMIN'` (never
`current_app_user_id()`), which is the correct, narrowest-possible
non-bypass mechanism for a background component that must be able to
progress ANY verification row, regardless of which admin originally
requested it — never a blanket "the worker ignores RLS" shortcut. A
future task type's own worker code must establish its own equivalent
trusted identity the same way; there is no shared "worker mode" flag
anywhere in this codebase that turns RLS off.

## Security Context & Authorization

Authorization happens ONCE, at the API/service boundary, before anything
is ever enqueued (`AuditService.StartVerification` calls
`authz.Service.HasPermission` before creating the `audit_verifications`
row or enqueueing anything) — the worker never assumes "a task exists,
therefore its operation was authorized". A job payload carries ONLY the
minimum trusted identifier the worker needs to reload authoritative state
from PostgreSQL (`VerifyAuditChainPayload{VerificationID}` — a single
UUID); it can never carry a client-supplied `user_id`/`role`, a JWT, a
password, an encryption key, or a MinIO credential, because no code path
in this package ever puts one there in the first place — every
`NewSomeTask` constructor this package will ever have is built the same
way `NewVerifyAuditChainTask` is: server-generated IDs in, nothing else.

## Resource Limits

- `serverConcurrency = 5` bounds total concurrent task execution across
  every queue (see "Queue Strategy").
- `defaultVerifyBatchSize = 1000` bounds how many audit-log rows a single
  batch loads into memory (unchanged from System 11 — see
  `docs/AUDIT_CHAIN.md`'s "Batching & large-chain strategy").
- `verifyAuditChainTimeout = 30m` bounds a single task attempt's maximum
  execution time — Asynq marks it failed (retried per its own budget,
  never left running forever) if exceeded.
- Document-processing operations that DO exist (upload, redaction) are
  bounded by `DocumentsConfig.MaxUploadSize`, applied symmetrically on
  both the write side (upload) and the read side (redaction's
  `readAllLimited`) — see "Task Types" above for why these stayed
  synchronous rather than needing a NEW background-specific limit.
- Temporary files: no task type in this package writes one. Redaction
  (which conceivably could, for a much larger image pipeline) works
  entirely in memory, bounded by the same upload-size limit above — see
  `document_redact.go`. If a future task type needs real temporary
  files, it must use a securely-created, unpredictable-name, non-world-
  readable temporary path, cleaned up via `defer` on every exit path
  (success AND failure) — no such path exists in this codebase today to
  audit against that standard.

## Rate Limiting / Abuse Protection

No dedicated rate-limiting middleware exists anywhere in this codebase
(Systems 1-11 built none), and master prompt is explicit not to invent a
completely separate rate-limiting system for this one. `POST
/audit/verify-chain`'s existing protections are: `audit:verify` RBAC
(ADMIN-only), and — more directly relevant to abuse — the database-level
`idx_audit_verifications_single_active` constraint, which makes it
structurally impossible for ANY number of concurrent callers (attacker or
not) to have more than one full-chain verification running at a time,
platform-wide. This is a real, load-bearing mitigation already in place,
not a gap this system leaves open.

## Job Observability

`internal/jobs/middleware.go`'s `LoggingMiddleware` — applied to every
task type via `NewMux` — is this package's one structured-logging point:
task type, task ID, queue, attempt number, start, completion, duration,
and (on failure) a safe `FailureCategory`. It never logs a task's payload
contents, a password, a JWT, an encryption key, a MinIO credential, or
document content — it has no access to any of those (every payload in
this package carries only trusted IDs — see "Security Context" above),
and reads only Asynq's own request metadata (`asynq.GetTaskID`/
`GetQueueName`/`GetRetryCount`/`GetMaxRetry`). This is a separate,
purely-operational log stream from the cryptographic audit trail (System
10) — see "Audit Integration" below for where the two connect and where
they deliberately do not.

## Audit Integration & Avoiding Recursive Audit Events

`AuditService.StartVerification`/`RunVerification` record exactly two
audit events per verification run — `AUDIT_CHAIN_VERIFICATION_REQUESTED`
(when accepted) and `AUDIT_CHAIN_VERIFICATION_COMPLETED` (when the run
finishes, whatever its outcome) — never one per batch, never one per row
read (unchanged from System 11; see `docs/AUDIT_CHAIN.md`'s "Avoiding
recursive audit-access events"). `LoggingMiddleware`'s own per-task
operational logging (above) is intentionally a SEPARATE stream from
this — a job starting/completing is not, by itself, a security event
worth a permanent audit-chain entry; only the domain-meaningful outcome
is.

## Testing

- `internal/jobs/errors_test.go` — `FailureCategory` classification,
  `Permanent`/`CategoryOf` round-tripping, `errors.Is(_, asynq.SkipRetry)`
  compatibility.
- `internal/jobs/middleware_test.go` — `LoggingMiddleware` passes success
  and failure through unchanged, and never panics without a real Asynq
  server context (a plain unit-test `context.Background()`).
- `internal/jobs/ids_test.go` — `DeterministicTaskID`/
  `AuditVerifyChainJobID` stability and non-collision.
- `internal/jobs/audit_verification_test.go` — unchanged System 11
  coverage (task construction, handler success/operational-failure/
  malformed-payload, error-handler retry-exhaustion logic), updated only
  where the shared infrastructure's own signatures changed.
- `internal/jobs/audit_verification_integration_test.go` (new,
  `-tags=integration`, real Redis) — proves, against a real broker, that
  the enqueued task lands on `QueueCritical` with the expected
  deterministic ID, and that a duplicate enqueue for the same
  verification ID is rejected (`asynq.ErrTaskIDConflict`) rather than
  silently creating a second task.
- `internal/httpserver/audit_flow_integration_test.go` — the full System
  11 regression: `TestAuditFlow_EndToEnd` (RBAC/IDOR, `202` ->
  `QUEUED`/`RUNNING` -> `VERIFIED`, tamper -> `INTEGRITY_FAILURE`, history/
  integrity-summary reflect it, `job_id` present and stable across the
  run) and `TestAuditFlow_SSE` (authenticated/authorized SSE stream,
  terminal event closes the connection) both continue to pass unchanged
  in behavior, now running on the refactored infrastructure.

Run: `go test ./...`, `go test -race ./...`,
`go test -tags=integration -p 1 ./...` (requires the docker-compose
postgres/redis stack up and migrated).

## Limitations & Follow-Up

- No task type in this package has genuine progress reporting other than
  `AUDIT_CHAIN_VERIFY` (`entries_checked`/`total_entries`) — master
  prompt's own instruction not to fabricate progress for a task with no
  meaningful information to support it is honored by there being no other
  task type to fabricate progress for.
- `QueueDefault` currently has zero consumers — reserved infrastructure,
  not a currently-exercised code path; see "Task Types" for why no task
  was invented just to exercise it.
- No scheduled/recurring job exists (stale-verification cleanup, expired-
  share cleanup, etc.) — master prompt's own instruction was to establish
  the infrastructure without inventing unnecessary recurring jobs; System
  11's `reconcileStale` already self-heals a stuck verification lazily,
  at read time, which was judged sufficient for the one stateful job type
  that exists today (see `docs/AUDIT_CHAIN.md`'s "Stale verification
  recovery"). A future genuine need (e.g. expired-share cleanup, if that
  ever becomes expensive enough to need one) would use Asynq's own
  periodic-task support, added to this SAME `internal/jobs` package.
