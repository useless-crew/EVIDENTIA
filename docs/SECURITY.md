# Security

## Purpose

TODO: Document the full security model for Evidentia. This file also
tracks what's implemented so far, distinct from the eventual full model.

## Implemented in System 1 (Foundation & Infrastructure)

- **Fail-closed configuration**: `internal/config` requires
  `DATABASE_USER`/`PASSWORD`/`NAME` and `MINIO_ACCESS_KEY`/`SECRET_KEY`/
  `BUCKET` explicitly — no baked-in default like `admin`/`password`. An
  invalid or incomplete configuration refuses to start rather than run in
  a partially valid state.
- **No wildcard CORS in production**: `CORS_ALLOWED_ORIGINS=*` is rejected
  at startup when `APP_ENV=production` (`internal/config/validate.go`).
- **Bounded HTTP timeouts**: every server timeout (read, write, idle,
  shutdown) is explicit — never zero/unbounded (`internal/httpserver`).
- **Request-size protection**: `SERVER_MAX_BODY_BYTES` bounds request
  bodies via `http.MaxBytesReader` (`internal/middleware/body_limit_middleware.go`).
- **Safe error responses**: panics and internal errors return a generic
  `INTERNAL_ERROR` message with no stack trace, SQL text, or file paths;
  the detail is logged server-side only (`internal/middleware/recovery_middleware.go`).
- **No secrets in logs**: structured request logging records method, path,
  status, duration, and request ID — never headers or request/response
  bodies, so it cannot log an `Authorization` header or a credential by
  construction (`internal/middleware/logging_middleware.go`).
- **Bounded, validated request IDs**: an oversized or malformed
  `X-Request-ID` is replaced rather than trusted
  (`internal/middleware/request_id_middleware.go`).

None of this is the full model below — it's the infrastructure layer later
systems build the rest on top of.

## Implemented in System 2 (Database & Data Layer)

Full detail in [docs/DATABASE_SCHEMA.md](./DATABASE_SCHEMA.md); summary
here:

- **PostgreSQL Row-Level Security**, enabled and `FORCE`d on every
  case/document/audit-adjacent table, with fail-closed behavior verified
  empirically (`backend/tests/db_rls_test.go`) — no application identity
  set means zero visible rows, never unrestricted access.
- **Transaction-local RLS identity**: `internal/repository.WithTx` sets
  `app.user_id`/`app.role` via `set_config(..., true)`, scoped to a single
  transaction — proven not to leak across transactions reusing the same
  pooled connection (`TestRLS_TransactionLocalIdentityDoesNotLeak`).
- **Audit-log append-only enforcement at the database level**: the
  runtime role (`evidentia_app`) holds `SELECT`+`INSERT` only on
  `audit_log` — no `UPDATE`, no `DELETE` — and does not own the table, is
  not a superuser, and does not have `BYPASSRLS`. All four verified by
  integration test (`backend/tests/db_audit_privileges_test.go`), not just
  asserted in the migration.
- **Least-privilege role separation**: migrations run as a privileged
  `DATABASE_MIGRATOR_USER`, distinct from `evidentia_app`, which the
  running server actually connects as and which owns nothing.
- **Audit-chain storage invariants** (not yet the chain logic itself — see
  Principles #7-#9 below): at most one genesis entry, at most one entry
  per predecessor hash, both enforced by constraints and verified to
  actually reject violations, not just declared.
- **Hash representation**: SHA-256-shaped columns (`documents.sha256_hash`,
  `audit_log.hash`/`prev_hash`, `compliance_certificates.document_hash`)
  are `BYTEA` constrained to exactly 32 bytes — no computation happens yet
  (System 7/8), but the storage can't represent a malformed hash.

## Implemented in System 3 (Authentication & Session Security)

Full detail in the Authentication section below; summary here:

- **Passwords**: bcrypt only (`internal/auth.HashPassword`/`VerifyPassword`,
  `golang.org/x/crypto/bcrypt`) — never SHA-256/MD5/plaintext. Cost is
  configurable (`BCRYPT_COST`, default 12) but `internal/config` rejects
  anything below 10.
- **JWT access tokens**: HS256, explicit algorithm allow-list (never
  `alg=none`, never an attacker-chosen algorithm), signature/issuer/
  audience/expiration all verified. Verified by test to reject: expired,
  malformed, wrong issuer, wrong audience, invalid/tampered signature,
  `alg=none`, and a token signed with a different (RS256) algorithm
  (`internal/auth/jwt_test.go`).
- **Refresh tokens are opaque, not JWTs**: a 256-bit random value, hashed
  (SHA-256) before storage — the raw value is never persisted. Rotation on
  every refresh; reuse of an already-rotated token revokes the entire
  token family, not just that token. Verified by integration test,
  including the exact replay scenario — login → refresh → reuse the OLD
  token → rejected (`internal/service/auth_service_integration_test.go`).
- **Re-resolved identity, not cached JWT claims**: every authenticated
  request re-reads the caller's current account status and roles from the
  database (`AuthService.ResolveIdentity`) — a deactivated user's
  still-unexpired access token stops authenticating on the very next
  request. Verified by test at both the service and middleware layers.
- **Generic authentication failures**: unknown email, wrong password, and
  inactive/suspended account all produce the exact same response —
  verified by test that the two hardest-to-distinguish cases (wrong
  password for a real account vs. an unknown email) produce byte-identical
  errors.
- **No client-supplied identity is ever trusted**: `X-User-ID`/`X-Role`/
  similar headers are never read by the auth middleware — identity comes
  only from a validated JWT plus the fresh database lookup. Verified by
  test.
- **Failed authentication is recorded**: via the `internal/audit.Recorder`
  interface — see "Audit integration" below; System 8's `ChainWriter` now
  durably persists this to the hash-chained `audit_log` table.

## Implemented in System 4 (Authorization — RBAC + ABAC + RLS Integration)

Full detail in the Authorization section below; summary here:

- **Centralized RBAC**: `internal/authz.Service.HasPermission` is the one
  place a role/permission decision is made — no handler or middleware
  hand-rolls its own role check. Roles and permissions are read fresh from
  the database (`roles`/`permissions`/`role_permissions`, seeded by
  `backend/db/seed/001_reference_data.sql`) on every call — never from the
  JWT's `role` claim, a request header, or any client-supplied value.
- **Multi-role union, not client selection**: a user holding several roles
  gets the union of every role's permissions
  (`internal/authz.Service.loadPermissions`); a client can never select
  which of their roles is "active" — verified by
  `backend/tests/rbac_test.go`'s `TestRBAC_MultiRoleUserGetsUnionOfPermissions`.
- **Centralized ABAC**: `internal/authz.Service.CanAccessCase`/
  `CanAccessDocument` evaluate RBAC first (cheap, no resource lookup — a
  request that fails here never pays for a database round trip), then the
  caller's actual relationship to the specific resource (case creator,
  active `case_members` row, or ADMIN). A document inherits its
  authorization scope entirely from its case — no independent document-
  level grant exists.
- **IDOR prevention**: a resource that doesn't exist and a resource the
  caller has no relationship to produce the identical decision and the
  identical HTTP response — verified by
  `backend/tests/abac_test.go`'s guessed-UUID and cross-case tests, and by
  `internal/middleware`'s
  `TestRequireCaseAccess_UnauthorizedAndNonexistentLookIdentical`.
- **RLS and application ABAC reinforce each other, not one trusting the
  other**: `CanAccessCase`/`CanAccessDocument` load resource context under
  the CALLER'S OWN transaction-local RLS identity (`repository.WithTx`)
  and then independently re-derive ownership/membership from the returned
  rows, rather than assuming "the query returned a row" already proves
  authorization.
- **Privilege-escalation guards**: `internal/authz.Service.CanModifyUserRole`
  requires the `user:role` permission (ADMIN only, per the seed data) AND
  independently blocks an actor from modifying their OWN role even if they
  hold that permission — verified by
  `TestRBAC_AdminCannotSelfEscalateThroughRoleModification`.
- **Protected witness information**: `internal/authz.CanViewProtectedPartyDetails`/
  `SanitizeInvolvedParty` restrict `case_involved_parties` records of
  `party_type = 'WITNESS'` to JUDGE, POLICE, and ADMIN — see "Protected
  information" below for what is and isn't enforced yet.
- **Deny-by-default authorization middleware**:
  `internal/middleware.RequirePermission` (RBAC) and `RequireCaseAccess`/
  `RequireDocumentAccess` (ABAC) fail closed on every ambiguous input — no
  authenticated user in context, a malformed resource ID, or an
  authorizer error all deny, never allow.
- **Authorization denials integrated with the existing audit
  abstraction**: every RBAC/ABAC denial calls `internal/audit.Recorder`
  (the same interface System 3 uses) with an `AUTHZ_DENIED` event —
  actor, action, resource type/ID, and an internal (never client-facing)
  reason code.

## Implemented in System 5 (Case Management & Case Lifecycle)

Full detail in "Case Management" below; summary here:

- **Case creation restricted at both layers**: `case:create` (RBAC, POLICE/
  ADMIN per the seed data) is checked by `middleware.RequirePermission` AND
  independently re-checked inside `CaseService.CreateCase` — a future
  caller of the service that bypasses HTTP entirely gets the same
  guarantee (see "Service-layer authorization" above, now exercised by a
  concrete caller for the first time).
- **`created_by` is never client-controlled**: `CreateCaseInput` has no
  field for it — the authenticated caller's own ID is the only value ever
  written, structurally, not just by convention. Verified by
  `TestCaseService_CreateCase_ClientSuppliedCreatedByIgnored` and
  `TestCaseFlow_EndToEnd`'s header/body-spoofing assertions.
- **Role-scoped case listing enforced by RLS, not Go-side filtering**:
  `GET /cases` runs `ListCasesFiltered` under the caller's own
  transaction-local RLS identity — the `cases_select` policy (System 2)
  already restricts the row set before any status/search/pagination filter
  in this query is even applied. Verified against real PostgreSQL by
  `TestCaseService_ListCases_RoleScoping` (POLICE/LAWYER/FORENSICS/JUDGE/
  ADMIN) and `backend/tests/case_rls_test.go`.
- **IDOR prevention extended to case detail/update**: `CaseService.GetCase`/
  `UpdateCase` return the identical `403` for a nonexistent case ID, an
  unrelated case, and a malformed UUID — never a `404` that would confirm
  existence. Verified by `TestCaseFlow_EndToEnd`.
- **Witness-identity redaction now actually wired into a live response**:
  `authz.SanitizeInvolvedParty` (built, unit-tested, but unused by any
  handler as of System 4) is now called by `CaseService`'s case-detail
  assembly before every involved-party record is serialized. Verified by
  `TestCaseService_GetCase_WitnessRedactedForForensics`.
- **Validated status-transition model**: `CaseService`'s own
  `caseStatusTransitions` map (not a System 2 schema feature — see "Case
  Management" below) rejects transitions like `OPEN` → `CLOSED` directly,
  inside the same transaction as the update, so a rejected transition
  never partially applies.
- **Audit integration without a false success event**: `CASE_CREATED`/
  `CASE_UPDATED`/`CASE_STATUS_CHANGED` are recorded only after their
  transaction commits; a validation failure, authorization denial, invalid
  transition, or duplicate `case_number` records none of them. Verified by
  every `TestCaseService_*_Denied`/`*Rejected`/`*Conflict` test asserting
  on a `spyRecorder`.

## Implemented in System 6 (Document Management & Evidence Ingestion)

Full detail in "Document Management" below; summary here:

- **Raw evidence bytes never touch PostgreSQL**: `documents.sha256_hash`/
  `storage_bucket`/`storage_object_key` are the only storage-related
  columns; the file itself is streamed straight to MinIO. No `BYTEA`
  column, no code path, stores or even transiently buffers a whole
  evidence file's content in the database.
- **True streaming, not memory-bound upload/download**: both directions
  (`DocumentService.UploadDocument`/`DownloadDocument`) move bytes via
  `io.Reader`/`io.TeeReader`/`io.Copy` chains — never `io.ReadAll` on an
  arbitrarily large file, and never Gin's buffer-to-memory-or-tempfile
  multipart parsing (`ParseMultipartForm`/`FormFile`).
- **Streaming SHA-256 at ingestion**: computed in the same pass as the
  object-storage write (one read of the source, two destinations via
  `io.TeeReader`), representing exactly the uploaded bytes — never a
  filename, metadata, or object key. Verified against known test vectors
  and streaming/buffered equivalence (`backend/pkg/hash/sha256_test.go`).
- **Server-generated, non-guessable storage identity**: object keys
  (`cases/{case_id}/documents/{document_id}/original`) are built entirely
  from server-resolved UUIDs — a client can supply neither the bucket nor
  the object key nor the case ID's authorization (that's still
  `CanAccessCase`'s job). The original filename is sanitized (path
  separators under both `/` and `\` conventions, control characters
  including CR/LF, length) but is display metadata only — it plays no
  role in storage addressing, so a hostile filename cannot become a path-
  traversal or header-injection vector.
- **Upload authorization is RBAC AND ABAC in one call**: `CanAccessCase(ctx,
  user, caseID, authz.ActionDocumentUpload)` — reused as-is from System 4,
  no new authorization code — checks `document:upload` (POLICE/FORENSICS/
  ADMIN per seed data) AND the caller's relationship to *this* case.
  LAWYER/JUDGE attached to a case still cannot upload (no `document:upload`
  grant); POLICE holding `document:upload` still cannot upload to another
  officer's unrelated case (no case relationship).
- **Download authorization never touches storage first**: `CanAccessDocument`
  resolves the document's case and authorizes it under RLS BEFORE
  `Storage.Get` is ever called — RLS protects PostgreSQL rows, not MinIO
  objects, so the database decision must always come first (master
  prompt §54). Cross-case LAWYER/FORENSICS access and guessed document
  UUIDs are denied identically to a nonexistent document — verified by
  `internal/service/document_service_integration_test.go` and
  `internal/httpserver/document_flow_integration_test.go`.
- **Never expose storage internals**: the document metadata DTO
  (`service.DocumentSummary`) has no `storage_bucket`/`storage_object_key`
  field, and no MinIO credential, connection string, or filesystem path
  ever reaches a client response or an operational log line.
- **Content-based MIME detection**: `http.DetectContentType` on the
  actual uploaded bytes, never the client-declared `Content-Type` header
  — stored and later served as the canonical `mime_type`.
- **Orphan-object handling, not silent loss**: a PostgreSQL insert failure
  after a successful object write triggers best-effort cleanup
  (`Storage.Delete`); a cleanup failure is logged operationally (ERROR,
  with case/document ID and object key) rather than left unhandled or
  silently swallowed — a failed upload is always reported as failed to
  the client, never a false success.
- **Audit integration**: `DOCUMENT_UPLOADED`/`DOCUMENT_DOWNLOADED` events
  via the same `internal/audit.Recorder` System 3/4/5 already use — never
  a second logging system, never document contents or storage credentials
  in the event metadata.

## Implemented in System 7 (Evidence Verification, Tamper Detection & Compliance Certificates)

Full detail in "Document Verification & Compliance Certificates" below;
summary here:

- **The canonical hash is the single source of truth, and it is
  immutable**: `POST /documents/:id/verify` and certificate generation
  both recompute SHA-256 from the object *actually retrieved from MinIO*
  and compare it against `documents.sha256_hash` — a client-supplied hash
  is never accepted as evidence of anything. Neither code path ever
  writes `sha256_hash`; a detected mismatch is reported, never "repaired".
- **Storage errors and integrity failures are never conflated**: an
  object that could not be retrieved/hashed at all returns a `503`
  service error; an object that *was* retrieved and hashed but no longer
  matches the canonical hash is a successful, meaningful verification
  result (`INTEGRITY_FAILURE`, `200`) — the two are structurally
  different outcomes, never confused in the response, the audit event, or
  the logs.
- **A tampered document can never receive a valid certificate**:
  `CertificateService.generateCertificate` re-verifies the document's
  hash immediately before signing — never trusting an earlier check —
  and refuses (`409`) on any mismatch.
- **Certificates are cryptographically bound to the exact hash they
  verified**, signed (ECDSA P-256) over a canonical, fixed-field-order
  payload — never arbitrary JSON, whose key order is not a stable
  contract — and a database-level uniqueness constraint on
  `(document_id, document_hash)` makes concurrent generation safe without
  relying on an application-level check-then-insert race.
- **Authorization is RBAC + ABAC at the service layer**, same pattern as
  Systems 5/6: `document:verify`/`certificate:read`/`certificate:create`
  (RBAC) plus `CanAccessDocument` (ABAC), independently re-checked inside
  `DocumentService`/`CertificateService`, not just HTTP middleware.
- **Audit integration reuses the existing `Recorder`**: `DOCUMENT_VERIFIED`,
  `DOCUMENT_INTEGRITY_FAILURE`, `CERTIFICATE_CREATED` — every one of
  these now durably persist through System 8's hash-chained
  `audit.ChainWriter` (see below), with no change to this system's code.

## Implemented in System 8 (Audit Trail & Cryptographic Audit Chain)

Full detail in [AUDIT_CHAIN.md](./AUDIT_CHAIN.md); summary here:

- **`audit.ChainWriter` replaces `audit.SlogRecorder`** as the
  `internal/audit.Recorder` implementation wired into `app.New` — the
  ENTIRE integration point. Every existing `recorder.Record` call across
  Systems 3-7 (login/logout/refresh, authorization denials, case
  create/update, document upload/download/verify/redact/share, admin user
  management) starts durably, tamper-evidently persisting to `audit_log`
  with no change to any of those call sites.
- **One canonical hash function** (`internal/audit.ComputeEntryHash`)
  computes SHA-256 over a fixed-field-order, labeled canonical string —
  never Go struct layout, map iteration order, or `json.Marshal`'s
  ordering on the entry itself — and is the ONLY function in the codebase
  that computes an audit entry hash; both the writer (on insert) and the
  verifier (on verification) call it, so the two can never drift apart.
  JSONB metadata is separately canonicalized (`CanonicalizeMetadata`,
  sorted keys, recursively) before being hashed or stored, since
  PostgreSQL's `jsonb` storage does not preserve input byte layout.
- **Genesis is the row with `prev_hash IS NULL`**, enforced unique by a
  partial unique index (`idx_audit_log_single_genesis`) established back
  in System 2's schema; every other entry's `prev_hash` equals its
  predecessor's own `hash`, and `idx_audit_log_prev_hash_unique` (also
  System 2) guarantees at most one entry may claim a given predecessor —
  the database-level fork-prevention invariant, not merely an
  application-level convention.
- **Concurrency safety is a PostgreSQL transaction-scoped advisory lock**
  (`pg_advisory_xact_lock`), acquired before reading the chain head and
  held for the whole transaction: at most one writer at a time can be
  "between" reading the current head and inserting the entry that claims
  it as predecessor. Verified by a concurrent-writers test (40 goroutines,
  run under `-race`) asserting the result is one unforked chain, not an
  application-level mutex (which would do nothing across pooled
  connections/processes anyway).
- **The chain-head lookup runs under an internal ADMIN-equivalent RLS
  identity**, not the acting user's own — `audit_log_select`'s RLS policy
  restricts a non-ADMIN identity to rows it owns (or a shared case), so
  reading the true chain head (which may belong to a different actor
  entirely) needs the policy's own already-legitimate unrestricted-
  visibility branch. This is not an RLS bypass: no other RLS-protected
  table is touched inside this transaction, and the row's own stored
  `role`/`user_id` columns still record the real actor, untouched.
- **Append-only at the database level**: `evidentia_app` holds `SELECT`,
  `INSERT` only on `audit_log` — no `UPDATE`, no `DELETE`, no
  `BYPASSRLS`, and does not own the table (verified by
  `backend/tests/db_audit_privileges_test.go`).
- **Chain verification streams in bounded-size batches**
  (`internal/audit.VerifyBatch`, called once per page), never loading the
  whole chain into memory. As of System 11, verification runs
  asynchronously (see "Implemented in System 11" below) rather than
  resuming via a `next_seq` HTTP parameter — the same batching, just no
  longer bounded by one request's lifetime.
- **`GET /audit`** is gated by `audit:read` RBAC, with row-level
  visibility beyond that left entirely to `audit_log_select` RLS — a
  filter can only narrow what RLS already permits, never widen it
  (verified against IDOR: an arbitrary `user_id`/`case_id` filter never
  returns another actor's rows). It records its own `AUDIT_ACCESSED` event
  once the query has already run, which cannot recurse (`Recorder.Record`
  only ever inserts one row and never calls back into `AuditService`).
  `POST /audit/verify-chain` and its companion routes are `audit:verify`
  (ADMIN-only) — see System 11 below.

## Implemented in System 11 (Audit Chain Verification & Integrity Dashboard)

Full detail in [AUDIT_CHAIN.md](./AUDIT_CHAIN.md)'s "Asynchronous
Verification & Integrity Dashboard"; summary here:

- **Reuses System 10's verifier completely**: `internal/audit.VerifyBatch`/
  `ComputeEntryHash`/`CanonicalizeMetadata` are unchanged and called by the
  new background job exactly as the old synchronous `VerifyChain` called
  them — one hash/canonicalization/chain-traversal implementation, never
  two.
- **Asynchronous by design**: `POST /audit/verify-chain` now dispatches a
  background job (Asynq, Redis-backed) and returns `202 Accepted`
  immediately, rather than verifying within the HTTP request — so a chain
  of any size never requires one long-running synchronous call.
  `audit_verifications` (migration `000005`) is the durable, PostgreSQL-
  authoritative record of every run — Redis is used ONLY as Asynq's queue
  transport, never as a competing state store for the result.
- **Duplicate-job prevention is database-enforced**:
  `idx_audit_verifications_single_active` — a unique index on a constant
  expression filtered to `status IN ('QUEUED', 'RUNNING')`, the identical
  idiom `audit_log`'s own genesis-uniqueness index already established —
  guarantees at most one verification runs at a time, independent of
  application-level locking.
  `TestAuditService_StartVerification_DeduplicatesActiveRun` proves 20
  concurrent callers all receive the same run.
  `MarkAuditVerificationRunning`'s `WHERE status = 'QUEUED'` guard
  similarly makes a redelivered/duplicate task a safe no-op, never a
  double-counted re-verification
  (`TestAuditService_RunVerification_ConcurrentInvocationsDoNotCorruptState`).
- **`FAILED` (operational) is never confused with `INTEGRITY_FAILURE`
  (cryptographic)**: a database outage or worker timeout marks a run
  `FAILED`; only a definite hash/link mismatch marks it
  `INTEGRITY_FAILURE`. Asynq retries an operational failure per its own
  configured budget; a `FAILED` status is only persisted once that budget
  is exhausted (`jobs.NewAuditVerificationErrorHandler`) — a transient
  blip that succeeds on retry never leaves a stray `FAILED` row.
- **Stale jobs self-heal on read, not via a second scheduled process**: a
  `QUEUED`/`RUNNING` row with no progress for longer than expected is
  reconciled to `FAILED`/`STALE_TIMEOUT` — and that correction is
  persisted — the first time anyone reads it
  (`AuditService.reconcileStale`), so a crashed worker can never leave a
  verification `RUNNING` forever.
- **SSE is authenticated exactly like every other route** (a normal
  bearer header — no token in the URL) and re-runs the SAME
  `audit:verify`+RLS check `GET /verify-chain/:id` uses before ever
  sending the caller a single byte of data — `verification_id` alone is
  never trusted as proof of authorization. (The handler registers with
  System 13's SSE manager before that check, not after, purely to avoid a
  narrow race where a fast verification's one completion event could be
  published in the gap between the check and the registration;
  registering itself discloses nothing since no event is ever forwarded
  before the check passes.) The manager's dispatch path never blocks,
  decoupling the verification worker from however slow or absent an SSE
  client is — see [REALTIME_EVENTS.md](./REALTIME_EVENTS.md) for the full
  System 13 security review (RBAC/ABAC/RLS on every SSE route,
  cross-case/cross-resource isolation, connection limits, and Redis's
  transport-only role).
- **Verification is structurally read-only against `audit_log`** — no
  code path in the job ever `UPDATE`s, `DELETE`s, reorders, or "repairs" a
  chain entry; a detected problem is reported, never fixed.
- **The dashboard is real, not simulated**: the pre-existing
  `/app/audit` "Blockchain Graph" tab's `setInterval`-driven fake
  verification sweep (built as scaffolding before this system's backend
  existed) is replaced with a genuine `POST`/poll-or-SSE/render-result
  flow against these endpoints — the frontend never computes `VERIFIED`
  or a progress percentage itself.

## Implemented in System 12 (Asynchronous Processing & Background Jobs)

Full detail in [BACKGROUND_JOBS.md](./BACKGROUND_JOBS.md); the security
review this system's own master prompt required, answered directly:

- **A client can never enqueue an arbitrary task, forge a user ID, or
  forge a role.** There is no generic `POST /jobs/execute` — every route
  is domain-specific (`POST /audit/verify-chain`), authorizes the caller
  BEFORE anything is created or enqueued
  (`AuditService.StartVerification` calls `authz.Service.HasPermission`
  first), and a job payload carries only a server-generated UUID
  (`VerifyAuditChainPayload{VerificationID}`) — never a client-supplied
  `user_id`/`role`/credential of any kind.
- **A client can never access another user's job** — `GET
  /audit/verify-chain/:id` re-runs the same RBAC check plus
  `audit_verifications`' own RLS (`current_app_role() = 'ADMIN'`) System
  11 already established; System 12 changes none of that.
- **The worker never bypasses RLS or uses `BYPASSRLS`.** It establishes
  its own transaction-local `app.user_id`/`app.role` context
  (`workerIdentity` in `internal/service/audit_service.go`) through the
  SAME `repository.WithTx` mechanism every HTTP-request-scoped call uses
  — see BACKGROUND_JOBS.md's "RLS in Workers". `evidentia_app` holds no
  `BYPASSRLS`, unchanged from every prior system.
- **A job payload can never contain credentials** — no task type this
  package defines has ever carried a JWT, password, refresh token,
  encryption key, or MinIO credential; every payload's own struct
  definition is small enough to audit by inspection
  (`VerifyAuditChainPayload` is one field).
- **Retries cannot duplicate business records** — `asynq.TaskID`
  (deterministic, per `jobs.DeterministicTaskID`) makes Asynq itself
  reject a duplicate enqueue for the same entity
  (`asynq.ErrTaskIDConflict`), underneath (never instead of) each task
  type's own database-level uniqueness constraint
  (`idx_audit_verifications_single_active` for `AUDIT_CHAIN_VERIFY`);
  `MarkAuditVerificationRunning`'s `WHERE status = 'QUEUED'` guard makes a
  redelivered task attempt a safe no-op, not a second, corrupting run.
- **A failed job can never remain `RUNNING` forever** — System 11's
  `reconcileStale` self-heals a stuck `QUEUED`/`RUNNING` row to `FAILED`
  the first time anyone reads it; System 12 changes nothing here.
- **A huge document cannot exhaust worker memory** — the one task type
  that exists (`AUDIT_CHAIN_VERIFY`) never loads a document at all;
  System 12 evaluated document-processing task types and deliberately did
  not introduce any (see BACKGROUND_JOBS.md's "Task Types") specifically
  because the existing synchronous paths are already bounded by
  `DocumentsConfig.MaxUploadSize` on both the write (upload) and read
  (redaction) side.
- **No temporary files exist to leak** — no task type in this package
  writes one (see BACKGROUND_JOBS.md's "Resource Limits").
- **Document-processing jobs cannot starve security jobs** — there is no
  document-processing job today; the queue-priority design
  (`QueueCritical` weight 6 vs `QueueDefault` weight 2) exists precisely
  so a future one couldn't, without needing to revisit this decision
  later.
- **Verification cannot modify audit records, and workers cannot create
  recursive audit events** — both unchanged from System 11 (see
  AUDIT_CHAIN.md's "Concurrency & idempotency" and "Avoiding recursive
  audit-access events"); System 12 adds no new code path that touches
  `audit_log` at all.
- **Redis is never the authoritative source for business state** — it is
  Asynq's queue transport only; PostgreSQL (`audit_verifications`) is the
  only place `AuditService` ever reads a verification's status from, and
  no other task type persists anything to Redis either.
- **An attacker cannot trigger unlimited expensive jobs** — no dedicated
  rate-limiting middleware exists in this codebase for any route (a gap
  master prompt explicitly says not to newly invent a whole system to
  close), but `POST /audit/verify-chain` is already bounded by
  `audit:verify` RBAC (ADMIN-only) and, more directly, by
  `idx_audit_verifications_single_active` — no number of concurrent
  callers can ever have more than one full-chain verification running at
  once, platform-wide.

## Implemented in System 13 (Real-Time Events & Server-Sent Events)

Full detail in [REALTIME_EVENTS.md](./REALTIME_EVENTS.md); the security
review this system's own master prompt required, answered directly:

- **No unauthenticated SSE connection is possible** — both
  `GET /audit/verify-chain/:id/events` and `GET /cases/:id/events` sit
  behind the same `authMW` every other route does; a request with no
  valid session is rejected `401` before anything else runs.
- **Every event is authorization-scoped, not just authenticated** —
  `internal/sse.Manager.Register` performs no authorization of its own;
  every caller re-runs the existing RBAC/ABAC/RLS machinery
  (`AuditService.GetVerification`'s `audit:verify`+RLS check;
  `middleware.RequireCaseAccess`'s `case:read`+case-membership/ownership
  ABAC check) BEFORE registering, and `Manager.dispatch` only ever
  delivers to the exact matching `resource_type:resource_id` scope — a
  client can never receive another case's or another verification's
  events. Verified live (an unrelated POLICE officer gets `403` opening a
  case's stream; ADMIN's pre-existing, established universal case-read
  access — the SAME the plain `GET /cases/:id` route already grants — is
  correctly preserved, not newly introduced) and by
  `TestCaseEvents_SSE_DeliversShareCreatedAndEnforcesIsolation`'s explicit
  cross-case check.
- **A client cannot subscribe to an arbitrary resource** — the scope key
  passed to `Register` is built from a URL path parameter that has
  ALREADY been independently authorized; no query parameter or
  client-supplied field can widen it.
- **Event payloads cannot leak sensitive document/witness data** — every
  event type's `data` shape (`internal/events/catalog.go`) was reviewed
  field-by-field: identifiers, hashes, and classified outcomes only —
  never raw document content, witness identity, a share's recipient or
  permission level, credentials, or internal error detail.
- **No JWT or credential ever appears in a URL** — both SSE clients
  (`EventStreamService`) use `fetch()` with a normal `Authorization:
  Bearer` header, never an `EventSource` with a token query parameter.
- **Redis is never reachable from the frontend and never authoritative**
  — it is Pub/Sub transport only (`internal/events.Channel`); PostgreSQL
  remains the source of truth for every fact an event describes, and a
  Redis outage degrades SSE only (REST continues functioning) rather than
  corrupting or blocking any persistent state.
- **A slow or malicious client cannot exhaust server memory or block
  other clients** — `internal/sse.Manager` bounds both per-connection
  buffering (`subscriberBufferSize`, oldest-drop-on-full, never blocking
  `dispatch`) and per-user concurrent connections
  (`maxConnectionsPerUser`, `429` beyond it).
- **Disconnected clients cannot remain registered forever** — `Register`'s
  `unsubscribe` is always deferred from `Stream`, releasing the
  connection's map entry and per-user count on every exit path (client
  disconnect, terminal event, or the periodic forced reconnect
  `maxConnectionDuration` causes for an otherwise-endless stream).
- **Verification events cannot recursively create audit records, and
  workers cannot bypass RLS** — both unchanged from Systems 10/11/12: an
  event notification is a wholly separate, non-durable signal from the
  cryptographic audit trail, and `AuditService`'s existing
  `workerIdentity` mechanism (not touched by this system) is what
  establishes RLS context for the PostgreSQL writes that precede every
  publish call.

## Principles

The eventual system will enforce all twelve of these. Implemented so far:
**1** (System 3), **2**/**3** (System 4), **4** (System 2), **7**/**8**/
**9** (System 8 — see above), **11** (System 3), **12** (every
security-sensitive action listed in "Audit integration" throughout this
document now durably records through System 8's hash chain, not only the
operational log). Partial: **5** (System 6 computes/persists the initial
hash; System 7 adds recompute-and-compare verification and tamper
detection — AES-256 encryption at rest, the other half of principle
**6**'s neighbor, remains unstarted). Not started: 6, 10.

1. JWT authentication
2. RBAC (Role-Based Access Control)
3. ABAC (Attribute-Based Access Control)
4. PostgreSQL Row-Level Security (RLS)
5. SHA-256 document integrity verification
6. AES-256 encryption at rest
7. Immutable, append-only audit logs
8. Hash-chained audit entries
9. Transactional / concurrency-safe audit writing
10. TLS in transit
11. Secure refresh-token handling (rotation, revocation)
12. Audit logging of all security-sensitive actions

## Authentication

System 3 establishes **who** is making a request. **What** they may do
with that identity is System 4's (RBAC/ABAC) job — deliberately not
implemented here (see "What System 3 does *not* do" below).

### Login flow

```text
POST /api/v1/auth/login {email, password}
  -> AuthService.Login:
       fetch user by email (no RLS on users — see DATABASE_SCHEMA.md)
       reject if account is not "active" (generic error either way)
       bcrypt.CompareHashAndPassword (outside any open DB transaction —
         bcrypt is deliberately slow; holding a transaction open across it
         would tie up a pool connection for no reason)
       load current roles, update last_login_at (one transaction)
       mint access token (internal/auth.JWTManager) + refresh token
         (internal/auth.GenerateRefreshToken) + session row (one
         transaction)
  -> {access_token, refresh_token, token_type, expires_in, user}
```

Every failure path (unknown email, wrong password, inactive/suspended
account) returns the identical `"Invalid email or password"` / `401`
response — see `internal/service/auth_service.go`'s `genericAuthError`.

### JWT access tokens

- **Library**: `github.com/golang-jwt/jwt/v5`.
- **Algorithm**: HS256 (shared secret) — see `config.JWTConfig`'s doc
  comment for why HS256 over RS256 was chosen for this project, and how a
  future move to RS256 would be scoped (key type + `jwt.SigningMethod`
  only; the claims/validation shape is signing-method-agnostic).
  `jwt.WithValidMethods` plus an explicit type-check in the keyfunc means
  `alg=none` and any non-HS256 token are rejected before the signing key
  is ever consulted.
- **Lifetime**: `JWT_ACCESS_TTL`, default 15 minutes, config-validated to
  be under 24 hours (never a multi-day access token).
- **Claims**: `sub` (user UUID — never email/username), `iss`
  (`JWT_ISSUER`, default `evidentia-api`), `aud` (`JWT_AUDIENCE`, default
  `evidentia-client`), `exp`/`iat`/`nbf`, `jti` (random per token), plus a
  non-standard `role` claim. **The `role` claim is a point-in-time
  snapshot, not an authorization source** — `internal/middleware/
  auth_middleware.go` re-resolves the caller's current roles from the
  database on every request via `AuthService.ResolveIdentity` rather than
  trusting it. It exists only so a client can display something without
  an extra round trip.

### Refresh tokens

- **Not a JWT.** A refresh token is 256 bits of cryptographically random
  data (`crypto/rand`), base64url-encoded — 43 characters, sent to the
  client once and never persisted in that raw form.
- **Storage**: `auth_sessions.token_hash` holds `SHA-256(raw token)`
  (`internal/auth.HashRefreshToken`) — a fast, non-adaptive hash is
  correct here (unlike a password) because 256 bits of entropy already
  makes brute-forcing the raw token infeasible regardless of hash speed;
  bcrypt would only add cost with no corresponding benefit.
- **Rotation**: every successful `/auth/refresh` revokes the presented
  session (`revoked_at`, `replaced_by`) and creates a new one. The
  presented token cannot be used again.
- **Reuse detection**: presenting an already-revoked token is treated as
  potential theft — the entire *token family* (`auth_sessions.family_id`,
  shared by every token descending from one login) is revoked, not just
  the one token presented. A legitimately-rotated sibling token becomes
  invalid too; this is intentional, conservative behavior, not a bug.
- **Lifetime**: `JWT_REFRESH_TTL`, default 7 days, config-validated to
  exceed `JWT_ACCESS_TTL`.

### Logout

`POST /api/v1/auth/logout` is the one auth route that **requires** a valid
access token (every other auth route is public — see "Public vs.
protected routes" below). This was a deliberate choice: logout is itself
an authenticated action, and the caller's own verified identity (not
merely "a session ID the client claims to own") determines which session
it's allowed to revoke — `AuthService.Logout` refuses to revoke a session
belonging to a different user than the one authenticated by the access
token, even if the correct raw refresh token is supplied. `refresh_token`
in the body is optional; omitting it is a no-op success, not an error
(there is nothing else to invalidate — access tokens are stateless and
short-lived by design, see "What System 3 does *not* do").

### Public vs. protected routes

`/auth/login` and `/auth/refresh` are intentionally public: the
credential/token presented *in the request body* is the authentication —
requiring a valid access token to reach the endpoint that *issues* access
tokens would be circular. `/auth/logout` requires one, per above. This is
the one place in System 3 where "protected" doesn't mean "behind
`middleware.Auth`" uniformly, and it's documented here precisely because
that asymmetry could otherwise look like an oversight.

### Auth middleware (`internal/middleware/auth_middleware.go`)

For every request it guards: extract `Authorization: Bearer <token>` →
`JWTManager.Validate` (signature, algorithm, issuer, audience, expiration)
→ parse `sub` as a UUID → `AuthService.ResolveIdentity` (fresh DB status +
role lookup) → attach `auth.AuthenticatedUser{ID, Email, Roles}` to the
request context via `auth.SetAuthenticatedUser`. Any failure at any step
produces the **same** generic `401 UNAUTHORIZED`; the specific reason
(`missing_header`, `token_expired`, `invalid_issuer`, `identity_unresolvable`,
...) is logged server-side only, never returned to the client.

### Audit integration

Failed authentication (`AUTH_LOGIN_FAILED`, `AUTH_REFRESH_FAILED`,
`AUTH_REFRESH_REUSE_DETECTED`) and successful security-relevant actions
(`AUTH_LOGIN_SUCCESS`, `AUTH_REFRESH_SUCCESS`, `AUTH_LOGOUT`) are recorded
through `internal/audit.Recorder` — an interface, not System 3's own
implementation of the durable audit trail. The concrete implementation
today, `audit.ChainWriter` (System 8), durably persists these to the
hash-chained `audit_log` table with correctly-computed `hash`/`prev_hash`
(see [AUDIT_CHAIN.md](./AUDIT_CHAIN.md)) — `AuthService` depended only on
the `Recorder` interface throughout, so wiring in the real writer required
no change to authentication code. `Recorder.Record` never returns an
error: a login or refresh must not fail merely because audit logging had a hiccup (see
master prompt §49) — this also means audit failures are currently
invisible to the caller by design, a tradeoff explicitly made here rather
than silently.

### What System 3 does *not* do

- **RBAC/ABAC** — `AuthenticatedUser.Roles` is populated; no code decides
  what a role may *do*. That's System 4.
- **Access-token revocation / blacklisting** — none exists. A short-lived
  access token (default 15 minutes) plus revocable refresh sessions is
  judged sufficient; a Redis-backed blacklist is explicitly out of scope
  for this system (Redis/Asynq business logic belongs to a later system).
- **Account lockout / rate limiting** — not implemented, to avoid an
  easy denial-of-service vector against legitimate users from a naive
  implementation. `AuthService`'s structure (one method per operation, no
  hidden global state) does not preclude adding this later.
- **MFA** — explicitly out of scope per the project requirements (a
  stretch goal for sensitive roles), not precluded by this architecture.

## Authorization

Authentication (System 3, above) answers "who is this user?". Authorization
(System 4, this section) answers "what may they do?" — a request may be
fully authenticated and still denied here. Every layer below fails closed:
missing, malformed, or ambiguous authorization information is always a
denial, never an allow.

```text
Request
  -> internal/middleware.Auth              (System 3 — identity)
  -> internal/middleware.RequirePermission (RBAC)
  -> internal/middleware.RequireCaseAccess / RequireDocumentAccess (ABAC)
  -> handler -> service
  -> repository.WithTx (SET LOCAL app.user_id / app.role, transaction-local)
  -> sqlc query -> PostgreSQL RLS
```

### Package layout

- `internal/authz` — the authorization engine. `Action` (a typed
  `resource:verb` string, mirroring `permissions.name` exactly — e.g.
  `case:create`), `Decision{Allowed, Reason}`, and `Service` (RBAC +
  ABAC). Depends on `internal/auth` (for `AuthenticatedUser`) and
  `internal/repository` (for `WithTx`); nothing depends back on it from
  those packages, so there is no import cycle.
- `internal/middleware/rbac_middleware.go` — `RequirePermission`.
- `internal/middleware/abac_middleware.go` — `RequireCaseAccess`,
  `RequireDocumentAccess`.

### RBAC — role/permission model

Roles and permissions are exactly System 2's schema — this system did not
rename or duplicate them. Roles: `ADMIN`, `POLICE`, `FORENSICS`, `LAWYER`,
`JUDGE` (`internal/models/role.go`). Permissions are `resource:action`
rows in the `permissions` table, granted to roles via `role_permissions` —
seeded by `backend/db/seed/001_reference_data.sql`, which is this
project's single source of truth for "what can each role do" (System 4
did not hardcode a second copy of that matrix in Go — `internal/authz`
reads it from the database on every check).

Baseline matrix (see the seed file for the authoritative version):

| Permission | ADMIN | POLICE | FORENSICS | LAWYER | JUDGE |
|---|---|---|---|---|---|
| `case:create` | ✓ | ✓ | | | |
| `case:read` | ✓ | ✓ | ✓ | ✓ | ✓ |
| `case:update` | ✓ | ✓ | | | |
| `document:upload` | ✓ | ✓ | ✓ | | |
| `document:read` | ✓ | ✓ | ✓ | ✓ | ✓ |
| `document:download` | ✓ | ✓ | ✓ | ✓ | ✓ |
| `document:verify` | ✓ | ✓ | ✓ | | |
| `document:redact` | ✓ | | | | |
| `document:share` | ✓ | ✓ | | ✓ | |
| `audit:read` | ✓ | ✓ | | ✓ | ✓ |
| `audit:verify` | ✓ | | | | |
| `certificate:read` | ✓ | | | | ✓ |
| `certificate:create` | ✓ | | | | |
| `user:create`/`user:update`/`user:deactivate`/`user:role` | ✓ | | | | |

`internal/authz.Service.HasPermission(ctx, user, action)` evaluates this:
for each of `user.Roles` (populated by System 3's `AuthService.ResolveIdentity`
— never client-supplied), it loads that role's permission set and unions
them. A user with **no** roles is denied every action without a database
call (`loadPermissions` short-circuits). A user holding **multiple** roles
gets the union of all of them — a client cannot select "which role is
active"; the server always evaluates the full set
(`TestRBAC_MultiRoleUserGetsUnionOfPermissions`).

`internal/middleware.RequirePermission(authorizer, action)` is the HTTP
integration point: it reads the already-authenticated
`auth.AuthenticatedUser` from the Gin context (never a header), calls
`HasPermission`, and returns `401` if no authenticated user is present,
`403` (`FORBIDDEN`) if the permission check fails, `500` if the authorizer
itself errors (never silently allowed), or calls the next handler.

### ABAC — resource/context policies

RBAC alone answers "can a POLICE user ever update a case?" — not "can
*this* POLICE user update *this* case". `internal/authz.Service.CanAccessCase`/
`CanAccessDocument` add that second, resource-specific check:

1. **RBAC first** (cheap, no resource lookup) — a role that lacks the
   permission entirely is denied before any database read of the
   resource, so an unauthorized action never pays for a resource lookup.
2. **Resource relationship** — `CanAccessCase` loads the case and the
   caller's own active `case_members` row *under the caller's own
   transaction-local RLS identity* (`repository.WithTx` with their real
   user ID/role), then independently checks: is the caller ADMIN, the
   case's creator (`cases.created_by`), or an active case member? A case
   invisible under RLS (wrong ID, or a real case the caller has no
   relationship to) is denied identically to one that plain doesn't
   exist — the `Decision.Reason` differs only for server-side
   diagnostics, never for what the client sees (see "IDOR prevention"
   below). `CanAccessDocument` first resolves the document's `case_id`
   (loading only document metadata — never file bytes from MinIO) and
   then applies the identical case-relationship check.

This is deliberately **not** "trust RLS and stop": loading the resource
under the caller's RLS identity and *then* independently re-deriving
`isOwner`/`isMember` from the returned rows means a future defect in
either layer (an RLS policy, or this Go logic) does not, by itself,
produce a wrong authorization decision — see "PostgreSQL RLS
integration" below for the full defense-in-depth rationale.

`internal/middleware.RequireCaseAccess`/`RequireDocumentAccess` parse a
path parameter as a UUID and call the corresponding `Can*` method. An
unparseable ID is denied with the exact same generic `403` a real
authorization failure produces (master-prompt-driven design: "missing,
malformed, ambiguous ... DENY", and never a response shape that lets a
client distinguish "bad ID" from "not yours").

### Case-based ABAC

`case_members` (System 2) is the authorization-relevant relationship
between a user and a case: `membership_type` records the functional role
(`OWNER`, `INVESTIGATOR`, `FORENSICS`, `LAWYER`, `JUDGE`, `VIEWER`), but
`CanAccessCase`'s relationship check treats "an active row exists" as
sufficient — it does not currently gate specific actions by
`membership_type` (e.g. a `VIEWER` row and a `LAWYER` row both satisfy the
relationship check equally). Combined with RBAC, this is still safe today:
the seed data's own permission grants are what prevent, say, a LAWYER from
updating a case (`LAWYER` holds no `case:update` permission at all,
regardless of membership type) — see `TestABAC_RBACGateBlocksBeforeResourceScope`.
A future system that needs finer-grained, membership-type-specific action
gating can add it to `CanAccessCase` without changing this contract.

Being attached to one case never implies access to another: LAWYER/
FORENSICS/JUDGE users must each be explicitly added to `case_members` for
a specific case (`TestABAC_LawyerUnrelatedCaseDenied`,
`TestABAC_ForensicsUnrelatedCaseDenied`, `TestABAC_JudgeUnauthorizedCaseDenied`).
A removed membership (`case_members.removed_at` set) is treated as no
membership at all (`TestABAC_RemovedMembershipDeniesAccess`).

### Document-based ABAC

A document has no independent access grant of its own — access is
inherited entirely from the caller's relationship to `documents.case_id`.
Uploading a document does not, by itself, grant the uploader standing
access if they are never made a case member
(`TestABAC_DocumentUploaderWithoutCaseRelationshipStillDenied`) — ownership
of the upload action is not a substitute for an ongoing case relationship
(master prompt: "ROLE PERMISSION AND RESOURCE RELATIONSHIP", never "ROLE
OR OWNERSHIP"). A document belonging to a case the caller has no
relationship to is denied identically to a document that does not exist
(`TestABAC_CrossCaseDocumentAccessDenied`, `TestABAC_GuessedDocumentUUIDDenied`).

### Protected information (witness identity)

`case_involved_parties` (System 2) records victims/witnesses/suspects/
accused/other parties on a case; its `metadata` column is documented as
sensitive at the schema level. `internal/authz.CanViewProtectedPartyDetails`
restricts a `WITNESS`-type record's identity-revealing fields (`display_name`,
`metadata`) to JUDGE, POLICE, and ADMIN — every other role
(FORENSICS, LAWYER) may see that a witness exists, never who they are.
`SanitizeInvolvedParty` performs the actual redaction and must be called
*before* serializing an involved-party record into any HTTP response.

As of System 5, `GET /cases/:id` (`CaseService`'s case-detail assembly) is
the first live caller — every involved-party record is passed through
`SanitizeInvolvedParty` before being added to the response, never after.
The schema still has no classification finer than `party_type`, so this
can only redact at the "is this a WITNESS record" granularity — a
finer-grained classification would need a schema change owned by whichever
system needs it.

### Privilege escalation / admin boundaries

Only `ADMIN` holds the `user:role` permission per the seed data, so
POLICE/FORENSICS/LAWYER/JUDGE are already denied at the RBAC gate before
`internal/authz.Service.CanModifyUserRole` even reaches its second check.
That second check is an explicit, RBAC-independent rule: **no actor may
modify their own role through this operation, even an ADMIN acting on
their own account** — this stays true even if `user:role` is ever granted
more broadly in the future, rather than depending on remembering to keep
this check in sync with the seed data. Verified by
`TestRBAC_OnlyAdminCanModifyRoles` and
`TestRBAC_AdminCannotSelfEscalateThroughRoleModification`.

No client-supplied role, permission, user ID, or admin flag is ever
trusted for this or any other decision — `X-Role`, `X-Permission`,
`X-User-ID`, and similar headers are not read anywhere in
`internal/authz` or `internal/middleware`; every input is either the
server-resolved `auth.AuthenticatedUser` or a path parameter interpreted
only as an opaque resource ID (`TestRequirePermission_IgnoresClientSuppliedHeaders`).

### IDOR prevention

Never assume possession of a UUID implies authorization. Two properties,
both verified by test:

1. A resource that doesn't exist and a resource the caller has no
   relationship to produce the **same** `Decision` shape (`Allowed:
   false`) and, at the HTTP layer, the **identical** response — status
   code and body — so a client cannot use the response to distinguish
   "this ID doesn't exist" from "this ID exists but isn't yours"
   (`TestRequireCaseAccess_UnauthorizedAndNonexistentLookIdentical`).
2. Cross-case document access, cross-user case access, and guessed UUIDs
   for both resource types are all denied
   (`backend/tests/abac_test.go`'s `TestABAC_GuessedCaseUUIDDenied`,
   `TestABAC_GuessedDocumentUUIDDenied`, `TestABAC_CrossCaseDocumentAccessDenied`,
   `TestABAC_NonMemberDeniedCaseAccess`).

### HTTP status behavior

- `401 UNAUTHORIZED` — no authenticated user in context (Auth rejected the
  request, or — a configuration defect — ran after this middleware
  instead of before it).
- `403 FORBIDDEN` — authenticated, but RBAC or ABAC denies the action.
  Always the single generic message `"You do not have permission to
  perform this action"` — never which permission, case, or document
  relationship failed (that detail lives only in `Decision.Reason` and the
  audit event, both server-side only).
- `500 INTERNAL_ERROR` — the authorizer itself failed (e.g. a database
  error) — treated as denied for the purposes of the response, never
  silently allowed.

### PostgreSQL RLS integration

RLS (System 2 — see `docs/DATABASE_SCHEMA.md`) is **defense-in-depth**,
not a replacement for the RBAC/ABAC layer above, and vice versa: `authz`
does not disable, bypass, or weaken any RLS policy, and every
`CanAccessCase`/`CanAccessDocument` resource load still runs through
`repository.WithTx`, exactly like every other System 2/3 query — there is
no "authorization bypass" query path anywhere in this system.

**Transaction-local identity, not connection-local**: `WithTx` (System 2)
sets `app.user_id`/`app.role` via `SELECT set_config(..., true)` inside
the same transaction that runs the protected query — the `true` argument
scopes both settings to that transaction only, so pooled-connection reuse
can never leak one request's identity into another
(`TestRLS_TransactionLocalIdentityDoesNotLeak`). System 4 introduces no
second, competing way to set this — `authz.Service` always goes through
the existing `repository.WithTx`.

**Effective role for RLS** (`internal/authz/identity.go`'s `effectiveRole`):
RLS's own policies (`current_app_role()`) only ever special-case
`'ADMIN'` — every other role is treated identically by RLS (case
membership is what actually gates access there). For a multi-role user,
System 4 sets `app.role` to `user.Roles[0]`, which — because
`AuthenticatedUser.Roles` is already sorted alphabetically by
`ListRolesForUser` — is `ADMIN` whenever the user holds it, with no
special case needed. This is a diagnostic/RLS-role-column convention
only; it never affects an RBAC/ABAC decision, which always evaluates the
user's **full** role set via `PermissionSet`'s union.

**Fail-closed** (verified by `backend/tests/db_rls_test.go`, unchanged by
System 4): no `app.user_id` means zero visible rows on every RLS-protected
table, never unrestricted access; `evidentia_app` holds no `BYPASSRLS` and
owns nothing.

### Service-layer authorization

`internal/authz.Service` is called directly by the ABAC middleware, but is
designed to also be called directly from a service or background job that
bypasses HTTP entirely (master prompt: "route middleware alone is not
sufficient — a future handler or background job could otherwise bypass
it"). It holds no mutable state beyond its `*pgxpool.Pool` and
`audit.Recorder` dependencies, and every decision is computed fresh from
the caller-supplied `auth.AuthenticatedUser` — never a package-level
variable — so it is safe under concurrent use by construction (verified
by `go test -race`).

### Audit integration

Every RBAC/ABAC denial (`Service.recordDenied`) is recorded through the
same `internal/audit.Recorder` interface System 3 already uses — an
`AUTHZ_DENIED` event carrying the actor's user ID and effective role, the
attempted `Action`, the resource type/ID (and case ID, for a document
denial), and an internal reason code (`permission_denied`,
`not_found_or_no_relationship`, `not_case_member`,
`self_role_modification_forbidden`). As with System 3, `Recorder.Record`
never returns an error and a recording failure never blocks or alters the
authorization decision itself (master prompt: audit failures must never
become an authorization bypass). `audit.ChainWriter` (System 8) is the
`Recorder` implementation today, durably persisting these denials to the
hash-chained `audit_log` table with no change required here.

### What System 4 does *not* do

- **Business logic for audit/admin** —
  `internal/handlers/audit` and `internal/service/audit_service.go` remain
  TODO stubs for a later system. Cases (System 5), document
  upload/download (System 6), verify/certificate (System 7), redact, and
  document sharing are all implemented today — see "Implemented in
  System 5"/"Implemented in System 6" above and "Case Management"/"Document Management"/
  "Document Verification & Compliance Certificates"/"Document Redaction"/
  "Document Sharing" below; every one of these used exactly the
  primitives (`app.App.AuthzService`,
  `RequirePermission`/`RequireCaseAccess`/`RequireDocumentAccess`) this
  system built, with no changes to `internal/authz` itself (document
  sharing's own delegated-access path is a new METHOD on the existing
  `authz.Service`, not a new authorization engine — see "Document
  Sharing" below).
- **Membership-type-specific action gating** — see "Case-based ABAC"
  above.
- **Finer-grained protected-information classification** beyond
  `party_type = 'WITNESS'` — see "Protected information" above.
- **The audit hash chain** — now implemented; see "Implemented in
  System 8" above and [AUDIT_CHAIN.md](./AUDIT_CHAIN.md).

## Case Management

System 5 (`internal/service.CaseService`, `internal/handlers/case`)
implements `POST /cases`, `GET /cases`, `GET /cases/:id`, `PUT /cases/:id`
— see [API_ENDPOINTS.md](./API_ENDPOINTS.md)'s Cases section for the full
request/response contract. This section covers the security-relevant
design decisions; business/API detail lives there.

### Service-layer authorization is not optional here

`CaseService` takes `*authz.Service` as a dependency and calls
`HasPermission`/`CanAccessCase` itself, in addition to (not instead of) the
HTTP-layer `RequirePermission`/`RequireCaseAccess` middleware already
guarding these routes. This is System 4's own documented design ("Service-
layer authorization" above) exercised for the first time by a real caller:
if a future background job or another service calls `CaseService` directly
without going through HTTP, it gets the identical authorization guarantee
a request would — there is no "internal, trusted" code path that skips the
check.

### Role-scoped listing: RLS does the work, not Go

`GET /cases` never runs an unscoped `SELECT` and filters in application
code. `ListCasesFiltered`/`CountCasesFiltered` (`db/queries/cases.sql`) run
inside `repository.WithTx` under the caller's own `app.user_id`/`app.role`
— System 2's `cases_select` RLS policy has already restricted the visible
row set (ADMIN: all; everyone else: `created_by = self` OR an active
`case_members` row) before the query's own status/search/date filters are
applied on top. POLICE/LAWYER/FORENSICS/JUDGE all resolve to the identical
policy — a police officer does not see "all cases" merely by holding the
POLICE role, only cases they created or are an active member of (the
specification's "police: own/all" is interpreted as "own, plus whatever
they're assigned to" — there is no separate agency/jurisdiction concept in
this schema to draw a wider "all" boundary from, and inventing one was
explicitly out of this system's scope). JUDGE has no dedicated docket
table either — it uses the same `case_members` mechanism, documented here
as a deliberately conservative placeholder for a future docket-specific
scope.

### Status transitions

`cases_status_check` (System 2) constrains `status` to a fixed set of
values but encodes no transition graph. `CaseService.caseStatusTransitions`
is this system's own conservative model:

```text
OPEN -> UNDER_INVESTIGATION -> SUBMITTED -> UNDER_REVIEW -> CLOSED -> ARCHIVED
```

with `SUBMITTED`/`UNDER_REVIEW` allowed one step back (a review can return
a case for further investigation) and `ARCHIVED` reachable directly from
any non-terminal status. Re-asserting a case's current status (no status
change, only e.g. a title edit) is always allowed. An invalid transition
is rejected with `400` inside the same transaction as the rest of the
update — it can never partially apply. This is explicitly a starting
point, not the final investigative workflow (see `ARCHITECTURE.md`'s
System 5 section).

### Case timeline is not a second audit system

`GET /cases/:id`'s `timeline` field is synthesized, at request time, from
already-loaded `cases.created_at`/`updated_at`, `documents.uploaded_at`,
and `case_involved_parties.created_at` — never read from `audit_log`, even
though System 8's `ChainWriter` now durably populates that table. This
avoids exactly the situation master-prompt-driven design explicitly warns
against: a second, competing "audit-like" table maintained by this
system — the real, authoritative security audit trail for this case is
`GET /audit?case_id=...` (see [AUDIT_CHAIN.md](./AUDIT_CHAIN.md)), and this
field never attempts to duplicate or replace it.

### Case creation transaction

`CaseService.CreateCase` runs entirely inside one `repository.WithTx` call:
insert the case row, insert the creator's `OWNER` `case_members` row (so
later reads/updates resolve through the same relationship mechanism every
other case member uses, not a `created_by`-only special case forever), and
only after that transaction commits does it call `audit.Recorder.Record`
for `CASE_CREATED`. A duplicate `case_number` (unique-constraint violation)
or any other database error rolls the whole transaction back and produces
no audit event at all — verified by
`TestCaseService_CreateCase_DuplicateCaseNumberConflict`.

## Document Management

System 6 (`internal/service.DocumentService`, `internal/handlers/document`)
implements `POST /cases/:id/documents` and
`GET /documents/:id/download` — see
[API_ENDPOINTS.md](./API_ENDPOINTS.md)'s Case Documents/Documents sections
for the request/response contract and
[STORAGE.md](./STORAGE.md) for the full upload/download pipeline
narrative. This section covers the security-relevant design decisions.

### Upload authorization: RBAC and case ABAC in a single call

`DocumentService.UploadDocument` calls
`authz.Service.CanAccessCase(ctx, user, caseID, authz.ActionDocumentUpload)`
— the exact same method `CanAccessCase` used for case read/update in
System 5, just with a different `Action`. That one call already
implements master prompt §10's "ACTION AND CASE ACCESS" requirement:
`HasPermission` checks `document:upload` first (POLICE/FORENSICS/ADMIN
per the seed data — LAWYER and JUDGE hold no `document:upload` grant at
all, so they are denied before any resource lookup), then the ABAC
relationship check confirms the caller is the case's creator, an active
`case_members` row, or ADMIN. No new authorization code was added for
System 6 — this is System 4's design paying off exactly as intended.

Like `CaseService`, `DocumentService` performs this check itself (not
just relying on `middleware.RequireCaseAccess` having already run) — see
"Service-layer authorization is not optional here" above.

### Download authorization: database before storage, always

`DocumentService.DownloadDocument` calls
`authz.Service.CanAccessDocument(ctx, user, documentID, authz.ActionDocumentDownload)`
— unchanged from System 4, resolving the document's `case_id` and
applying the identical case-relationship check. Critically, the sequence
is always: authorize → load the document row under RLS → **only then**
call `Storage.Get`. PostgreSQL RLS has no equivalent protection over
MinIO objects, so a hypothetical "fetch the object, then decide" ordering
would mean the object had already left the authorization boundary before
any check ran. This is verified structurally (the code has no path that
calls `Storage.Get` before `CanAccessDocument` returns `Allowed`) and
behaviorally (`TestDocumentService_DownloadDocument_CrossCaseLawyerDenied`/
`ForensicsCrossCaseDenied`/`GuessedUUIDDenied` never observe a storage
call for a denied request).

### Storage identity is entirely server-generated

A document's object key — `cases/{case_id}/documents/{document_id}/original`
— is built from two UUIDs the client never controls: `case_id` comes from
the already-authorized route parameter, and `document_id` is generated
fresh (`uuid.New()`) before the file is ever streamed. There is no
request field for `bucket`, `object_key`, `uploader_id`, or `sha256_hash`
in `UploadDocumentInput`/the multipart contract — a client cannot supply
an authoritative value for any of them even if it tried, because no field
exists to bind one into. The original filename is sanitized
(`sanitizeFilename`) purely as DISPLAY metadata (`documents.filename`,
and the `Content-Disposition` header on download) — path separators under
both `/` and `\` conventions and control characters (including CR/LF,
closing off `Content-Disposition` header injection) are stripped
regardless, but even an unsanitized filename could not have affected
storage addressing, since the object key never incorporates it.

### Streaming, not buffering

Both directions move bytes via `io.Reader` chains, never
`io.ReadAll`/Gin's `ParseMultipartForm` buffer-then-forward behavior — see
[STORAGE.md](./STORAGE.md#document-upload-pipeline-implemented--system-6)
for the exact `io.TeeReader`/`limitedReader` construction. This keeps
memory usage roughly independent of file size and lets the SHA-256 hash
be computed in the same pass as the object-storage write, guaranteeing it
represents exactly the bytes that were stored (never a second, possibly
divergent read of the file).

### Size limits: two independent guards, one response

`middleware.BodyLimit(DocumentsConfig.MaxUploadSize)` caps the whole HTTP
request (multipart overhead included) before the handler even starts
parsing; `DocumentService`'s `limitedReader` separately caps just the
`file` part's byte stream during hashing/storage. Either guard tripping
produces the identical `413 REQUEST_ENTITY_TOO_LARGE` response
(`internal/handlers/document/upload.go`'s `writeMultipartReadError`
detects `*http.MaxBytesError` specifically so the coarse guard doesn't
leak as a generic `400`) — a client cannot distinguish which layer caught
an oversized upload, and neither guard alone is trusted as sufficient
(defense in depth, matching this project's RLS-plus-application-ABAC
posture elsewhere).

### Upload atomicity and orphan handling

PostgreSQL and MinIO do not share a transaction. `UploadDocument` writes
to object storage FIRST, then persists the PostgreSQL row — never the
reverse, so a committed document row always refers to bytes that
genuinely exist. If the PostgreSQL insert fails after a successful
object write, `cleanupOrphan` best-effort deletes the object; a deletion
failure is logged operationally (ERROR, with the case/document ID and
object key) for manual reconciliation rather than silently accepted. In
every failure path — validation, authorization, streaming, storage, or
database — the client sees a failure response and no `DOCUMENT_UPLOADED`
audit event is recorded; there is no code path that reports success
without a durable, retrievable document.

### Content-type handling

`http.DetectContentType` inspects the first 512 bytes of the actual
upload stream; the client's declared `Content-Type` on the file part is
read nowhere. The detected type becomes `documents.mime_type` and is
later returned as the download response's `Content-Type` — paired
unconditionally with `Content-Disposition: attachment` and
`X-Content-Type-Options: nosniff`, so a browser is never invited to
render or execute evidence content inline, regardless of what type it
turns out to be.

### Audit integration

`DOCUMENT_UPLOADED` (on successful upload) and `DOCUMENT_DOWNLOADED` (once
the object stream is confirmed retrievable, not after the client finishes
reading it) are recorded through the same `internal/audit.Recorder`
interface System 3/4/5 already use — metadata includes filename,
document_type, file_size, mime_type, and the hex-encoded SHA-256 hash,
and deliberately never document contents or storage credentials.

### What System 6 does *not* do

- **Hash verification/tamper detection** — System 6 computes and persists
  the *initial* SHA-256 hash only; recomputing and comparing a stored
  object's current hash against `documents.sha256_hash` to detect
  tampering is System 7's job (`POST /documents/:id/verify` — see
  "Document Verification & Compliance Certificates" below).
- **Redaction/derivative documents** — this system's storage layout
  (original object never overwritten, `documents.parent_document_id`
  already present in the schema but unused by any query System 6 added)
  was deliberately left compatible with a later redaction system creating
  a new document row + new object without modifying the original — see
  "Document Redaction" below for that system, now implemented on exactly
  this foundation.
- **The audit hash chain** — now implemented (System 8, see
  [AUDIT_CHAIN.md](./AUDIT_CHAIN.md)); `DOCUMENT_*` events went through
  the same interface-based `Recorder` all along, so no change was
  required to `DocumentService` when the real hash-chained writer was
  wired in.
- **Compliance certificates, document sharing** — certificates are now
  System 7's job (below); document sharing is now also implemented (see
  "Document Sharing" below), built on the storage layout this system
  established (a share never touches an object or a hash — see that
  section's "Sharing must never change document integrity").

## Document Verification & Compliance Certificates

System 7 (`internal/service.DocumentService.VerifyDocument`,
`internal/service.CertificateService`, `internal/handlers/document/{verify,certificate}.go`)
implements `POST /documents/:id/verify` and
`GET /documents/:id/certificate` — see
[API_ENDPOINTS.md](./API_ENDPOINTS.md)'s Documents section for the
request/response contract. This section covers the security-relevant
design decisions. The core question this system answers, and the only
one it answers:

> Can Evidentia prove that the evidence currently stored is exactly the
> evidence that was originally ingested?

```text
documents.sha256_hash (canonical, written once at upload — System 6)
        |
        v
   Storage.Get(bucket, object_key)  ──stream──>  pkg/hash (SHA-256)
        |                                              |
        |                                       computed_hash
        v                                              |
   bytes.Equal(computed_hash, documents.sha256_hash) <──┘
        |
        +── match ────> VERIFIED / certificate generation proceeds
        |
        +── mismatch ─> INTEGRITY_FAILURE / certificate generation refused
                         (documents.sha256_hash is NEVER rewritten)
```

### The canonical hash is authoritative and immutable

`documents.sha256_hash`, set once at upload (System 6), is the only value
either verification or certificate generation ever compares against — a
client-supplied hash is never accepted (there is no request field for
one on `POST /documents/:id/verify`, which takes no body). Neither
`VerifyDocument` nor `CertificateService.generateCertificate` contains
any code path that writes `documents.sha256_hash`: a discovered mismatch
is reported and audited, never "repaired" by overwriting the canonical
value with whatever was found in storage. This is the single most
important invariant System 7 protects — silently "fixing" the hash after
a mismatch would destroy the platform's ability to ever prove tampering
occurred.

The one column verification/certificate generation *may* write is
`documents.status`: `reconcileTamperStatus` (shared by both entry points,
so a discovered mismatch is handled identically regardless of which
one found it) moves it to `TAMPERED` on a mismatch, or back to `ACTIVE`
if a previously-tampered document re-verifies successfully (e.g. after an
operator restores the object from backup) — always reflecting the
*current* truth, and only issuing an `UPDATE` when the value actually
needs to change, so repeated identical-outcome verifications don't churn
the row.

### Storage errors vs. integrity failures — never conflated

A storage error (the object could not be retrieved or hashed at
all — MinIO unreachable, the object missing) is returned as an
`*utils.AppError` (`503`), exactly like any other service-layer failure.
An integrity failure (the object *was* retrieved and hashed successfully,
but the digest differs from the canonical hash) is a **successful**
verification call that *found* tampering — returned as a normal,
nil-error result with `status: "INTEGRITY_FAILURE"`, structurally
identical to a `VERIFIED` result except for which status string it
carries. A caller (or this codebase's own tests —
`TestDocumentService_VerifyDocument_MissingObjectReturnsStorageError` vs.
`_ModifiedObjectReturnsIntegrityFailure`) must never confuse "the request
failed" with "the request succeeded and reports tampering": conflating
them would let a transient storage outage masquerade as evidence
tampering, or vice versa.

### Streaming, shared with the upload/download path

`recomputeDocumentHash` (`internal/service/document_service.go`) streams
the retrieved object through `pkg/hash` via `io.Copy` — never
`io.ReadAll` — the same streaming discipline
[STORAGE.md](./STORAGE.md) documents for upload/download. It is a
package-level function, not a method on either service, specifically so
`DocumentService.VerifyDocument` and `CertificateService.generateCertificate`
share the identical hashing logic without either service depending on
the other — a discovered mismatch is computed and reported the same way
regardless of which entry point triggered it.

### Authorization: RBAC + ABAC, independently re-checked at the service layer

`VerifyDocument` calls `authz.Service.CanAccessDocument(ctx, user,
documentID, authz.ActionDocumentVerify)` — the same pattern System 6
established for download, requiring `document:verify` (POLICE/FORENSICS/
ADMIN per the seed data — note JUDGE and LAWYER hold no `document:verify`
grant, so neither can trigger verification even when attached to the
case) AND the caller's relationship to the document's case.

Certificate access is a three-way split, entirely server-side:
`GetOrCreateCertificate` first checks `certificate:read`
(JUDGE/ADMIN per seed data); if an existing certificate is found for the
document's current hash, it is returned. Otherwise, a **second**,
independent `CanAccessDocument` check for `certificate:create`
(ADMIN only) decides whether to attempt generation — a caller who holds
only `certificate:read` and finds no certificate gets a `404`,
indistinguishable from "not generated yet", so the create/read permission
split is never leaked to the client as a different response shape.
Neither check is HTTP-middleware-only: `middleware.RequireDocumentAccess`
gates the route with `certificate:read` (the minimum needed to reach the
handler at all), and `CertificateService` re-derives both decisions
itself before doing anything — see "Service-layer authorization is not
optional here" above.

Both services resolve the document row under the caller's own
transaction-local RLS identity (`repository.WithTx`/`AppIdentity`) before
touching MinIO — the same "database before storage, always" ordering
System 6 established for download (see above): a denied caller's request
never reaches `Storage.Get`, and a guessed/nonexistent document ID is
denied identically to a real, unrelated one (verified by
`TestDocumentService_VerifyDocument_GuessedUUIDDenied` and the
certificate suite's equivalent cases).

### Certificates: cryptographically bound to the exact hash, never issued for tampered evidence

`CertificateService.generateCertificate` re-verifies the document's
integrity immediately before signing — it never trusts an earlier
verification result, even one from moments ago — and refuses
(`utils.ErrConflict`, `409`) if the recomputed hash no longer matches the
canonical hash. A certificate is signed over a canonical, deterministic
payload (`canonicalCertificatePayload` — fixed field order:
`certificate_id`, `document_id`, `document_hash`, `certificate_version`,
`issued_at`, `issuer`, `generated_by`; never arbitrary JSON marshaling,
whose key order is not a stable contract in Go or any language) using
ECDSA P-256 (`pkg/crypto.SignECDSA`, ASN.1 DER signature over the
payload's SHA-256 digest). The `issued_at` timestamp is truncated to
microsecond precision *before* signing — PostgreSQL `timestamptz`'s own
resolution — so the value that gets signed and the value later read back
from `compliance_certificates.generated_at` are bit-for-bit identical;
without this, a signature computed over Go's full nanosecond-precision
`time.Now()` would spuriously fail re-verification after every database
round-trip (`generated_at` always loses those trailing digits on
persist). `CertificateService.VerifyCertificateIntegrity` reconstructs
that same payload from the persisted row and independently checks both
`certificate.document_hash == document.sha256_hash` and the signature —
a certificate is never treated as valid merely because its database row
exists (`TestCertificateService_VerifyCertificateIntegrity_TamperedSignatureFails`).

The signing key (`CertificateConfig.SigningKeyPEM`, read from
`CERTIFICATE_SIGNING_KEY`) is a PEM-encoded PKCS#8 ECDSA private key,
never hardcoded, never logged, and never reachable through any API
response (`CertificateSummary` carries only the resulting signature, not
the key). If unset, `NewCertificateService` generates a fresh,
process-lifetime-only key rather than refusing to start (logged once at
`WARN` so an operator notices) — certificate signing is an enhancement
over the certificate's core guarantee (exact-hash binding), not a
prerequisite for it; a misconfigured (unparseable) configured key,
by contrast, fails construction outright rather than silently falling
back to an insecure default.

### Concurrency: database-level uniqueness, not an application-level race

Two simultaneous "generate a certificate for this document" requests
cannot both succeed: `compliance_certificates_document_hash_unique`
(a `UNIQUE (document_id, document_hash)` constraint,
`db/migrations/000003_certificate_integrity.up.sql`) backs
`CreateCertificate`'s `INSERT ... ON CONFLICT ... DO NOTHING`. A losing
request's `INSERT` returns zero rows rather than an error; the caller
treats that as "already exists" and fetches the winning row
(`GetCertificateByDocumentAndHash`) — both requests return the identical
certificate, never a duplicate row and never a hard error for the loser
(`TestCertificateService_GetOrCreateCertificate_ConcurrentGenerationProducesOneCertificate`
issues five concurrent requests and asserts they all resolve to one
certificate ID).

### Audit integration

`DOCUMENT_VERIFIED`/`DOCUMENT_INTEGRITY_FAILURE` (verification) and
`CERTIFICATE_CREATED` (successful certificate generation; a mismatch
discovered during generation records `DOCUMENT_INTEGRITY_FAILURE`
instead, identically to a direct verify call) go through the same
`internal/audit.Recorder` interface every prior system uses — event
metadata carries the hex-encoded hashes involved, never raw file bytes or
storage credentials, and no second logging system was introduced; the
hash-chain logic itself lives entirely in System 8 (see below).

### What System 7 does *not* do

- **The audit hash chain** — `DOCUMENT_*`/`CERTIFICATE_*` events go
  through the existing interface-based `Recorder`; computing and verifying
  the hash chain over `audit_log` is System 8's job (see "Implemented in
  System 8" above and [AUDIT_CHAIN.md](./AUDIT_CHAIN.md)).
- **Redaction, document sharing** — both are now implemented (see
  "Document Redaction" and "Document Sharing" below), built on top of
  exactly the verify/certificate independence this system established —
  a redacted derivative gets its own certificate, bound to its own hash,
  and a shared document's certificate is reachable by its recipient
  (per the share's permission) with no change to this system's code.
  System 7 itself preserves the original object/hash exactly as System 6
  left them.
- **A public certificate-verification HTTP endpoint** —
  `CertificateService.VerifyCertificateIntegrity` provides the capability
  (used directly by this system's own tests), but no route exposes it
  publicly today; `GET /documents/:id/certificate` returns the
  certificate's own record, which already carries everything a caller
  needs to verify it independently if a future system adds that route.
- **AES-256 encryption at rest, PDF/legal-format certificate rendering,
  a blockchain, or any output format beyond the JSON API response** — all
  explicitly out of this system's scope.

## Document Redaction

`internal/service.DocumentService.RedactDocument`,
`internal/handlers/document/redact.go` implement
`POST /documents/:id/redact` — see [API_ENDPOINTS.md](./API_ENDPOINTS.md)'s
Documents section for the request/response contract. This section covers
the security-relevant design decisions. The core guarantee this system
provides:

> A redaction is never an edit to the original evidence. It is a new,
> cryptographically independent derivative — the original's row, object,
> hash, and any existing certificate are never modified.

```text
source document (documents row A, hash H1, object at cases/.../A/original)
        |
        | 1. authz.CanAccessDocument(user, A, document:redact)
        | 2. recompute A's hash from its CURRENT stored object,
        |    compare to H1 — refuse (409) on mismatch, exactly the
        |    same anti-tamper check certificate generation performs
        | 3. decode A's bytes as an image (refuse, 422, if the
        |    mime_type has no supported redaction implementation)
        | 4. destructively overwrite each requested region's pixels
        |    (opaque black, draw.Src — a straight replace, never an
        |    alpha-blended overlay) on an IN-MEMORY COPY
        | 5. re-encode, compute H2 (server-side only — never
        |    client-supplied), upload as a NEW object
        v
derivative document (documents row B, hash H2, parent_document_id = A,
                      object at cases/.../B/original)
        |
        +── redactions row: source_document_id=A, result_document_id=B,
             region_data, reason, created_by
```

`A` is never touched by any step above — not read-modify-written, not
even its `status`/`metadata`. `POST /documents/{A}/verify` and
`GET /documents/{A}/certificate` behave identically before and after the
redaction; so do the equivalent calls against `B`, which is a completely
ordinary `documents` row from every other route's perspective.

### Authorization: no new permission granted

`document:redact` was already seeded (System 2/4) but held by **no
role except ADMIN** until this system existed to exercise it — this
system reuses that existing grant rather than expanding it.
`backend/tests/rbac_test.go`'s `TestRBAC_PolicePermissions` explicitly
asserts POLICE does **not** hold `document:redact`, matching master
prompt guidance for this system: "do not grant new permissions merely
because redaction requires it." `RedactDocument` calls
`authz.Service.CanAccessDocument(user, sourceID, authz.ActionDocumentRedact)`
— the identical RBAC-permission-AND-case-relationship pattern
verify/download/certificate already use, independently re-checked at the
service layer regardless of what HTTP middleware already decided.

### Only two formats get REAL redaction — everything else is refused

The single most important constraint on this system: a "redacted"
document must not merely *look* redacted. `RedactDocument` supports
**exactly** `image/png` and `image/jpeg` (the document's server-detected
`mime_type` from upload — System 6 never trusts a client-declared
Content-Type). For these, `image/draw`'s `draw.Src` compositing operator
performs a genuine pixel REPLACE (not an alpha blend) on a decoded,
in-memory copy before re-encoding — the original pixel values are
provably gone from the derivative's bytes, verified directly by
`TestRedactDocument_ContentActuallyRemoved` (decodes the derivative's
actual re-encoded bytes and asserts the redacted region reads back as
pure black, never the original color).

Every other `mime_type` — including `application/pdf`, the format most
real-world "redaction" tooling actually targets — is refused with `422`.
This project has no library in its approved stack (see
`TECH_STACK.md`) capable of safely stripping underlying text/vector
content from a PDF; drawing a black box merely on top of an
otherwise-unmodified PDF would still leak the "redacted" content to
anyone who extracts its underlying text, which is **worse** than
refusing the request outright — master prompt guidance is explicit that
a fake/incomplete redaction must never be presented as a real one.
Extending this list to another format requires actually implementing
(and testing, the same way) genuine content removal for it, never adding
a permissive map entry.

### Integrity is re-verified before every redaction

Before processing, `RedactDocument` retrieves the source's *current*
stored object and recomputes its SHA-256, comparing it against the
canonical `documents.sha256_hash` — the identical check
`CertificateService.generateCertificate` performs before issuing a
certificate, shared via the same `reconcileTamperStatus`/
`recomputeDocumentHash` helpers. A mismatch refuses with `409` rather
than silently deriving a "redacted" copy from bytes that no longer match
what was actually ingested — laundering an undetected tampering event
into a seemingly-clean new document would be far worse than simply
verifying the document first, which System 7 already made cheap to do.

### The derivative's hash is always server-computed, always different

`H2` (the derivative's `sha256_hash`) is computed by this system, in
memory, from the actual re-encoded bytes it is about to upload — there is
no request field for a client-supplied hash anywhere in
`POST /documents/:id/redact`'s contract. As a final defense-in-depth
check, `RedactDocument` explicitly refuses (rather than silently
persisting) the pathological case where `H2` would equal `H1` — not
reachable given regions are validated as non-empty with positive area,
but never assumed safe by omission.

### Storage: a new object, never an overwrite

The derivative is written to a brand-new object key
(`documentObjectKey(caseID, derivativeID)` — the exact same
System-6 helper/convention every original upload already uses, just with
a fresh, server-generated document ID) — never the source's key. A
storage write that succeeds followed by a failed PostgreSQL transaction
triggers the same best-effort orphan-object cleanup `UploadDocument`
already established (`DocumentService.cleanupOrphan`); a transaction that
never runs because storage failed leaves no document/redaction row
pointing at a nonexistent object.

### Derivative access control and lineage

The derivative inherits the **same** case as its source (`case_id` is
copied, never re-derived from anything client-supplied), so
`CanAccessDocument` applies the identical case-relationship rule to it as
to any other document in that case — "the derivative exists" never
implies "everyone can now read it"
(`TestRedactDocument_DerivativeAccessIndependentlyControlled`). Lineage is
explicit and queryable both directions: `documents.parent_document_id`
(now also surfaced as `DocumentSummary.parent_document_id` in every API
response that returns document metadata) points from derivative to
source; `redactions.source_document_id`/`result_document_id` (with a
database-level `UNIQUE` constraint on `result_document_id` — a document
row is the output of at most one redaction) link them the other way,
alongside `reason`, `created_by`, and `region_data`.

### Audit

Every successful redaction records a `DOCUMENT_REDACTED` event (source/
result document IDs, reason, region count, both hashes hex-encoded —
never raw file bytes) through the same `internal/audit.Recorder`
interface every prior system uses; a mismatch discovered during the
pre-processing integrity check records `DOCUMENT_INTEGRITY_FAILURE`
instead, identically to a direct verify call. No cryptographic audit-chain
logic was introduced here — that remains a separate, later system's job,
exactly as System 6/7 already established for their own `DOCUMENT_*`/
`CERTIFICATE_*` events.

### What this system does *not* do

- **PDF or any non-raster-image redaction** — see above; refused safely,
  never faked.
- **The audit hash chain** — unchanged.
- **A standalone `GET /documents/:id`** — remains out of scope; see
  API_ENDPOINTS.md's "Not yet implemented" (document sharing, a
  once-planned "not yet" item here, is now implemented — see "Document
  Sharing" below).
- **Expanding who may redact** — `document:redact` remains ADMIN-only,
  per existing System 4 policy; this system does not touch
  `role_permissions`.
- **Asynchronous/background processing** — redaction here is synchronous,
  bounded by the same `MAX_UPLOAD_SIZE` originals are (an in-memory
  decode/re-encode is unavoidable for real pixel-level content removal);
  Asynq remains unintroduced, per `TECH_STACK.md`.

## Document Sharing

`internal/service.ShareService`, `internal/handlers/document/share*.go`,
`internal/handlers/shared`, `internal/handlers/user.Search` implement
`POST /documents/:id/share`, `GET /documents/:id/shares`,
`POST /documents/:id/shares/:shareId/revoke`, `GET /shared/documents`, and
`GET /users/search` — see [API_ENDPOINTS.md](./API_ENDPOINTS.md)'s
Documents section for the request/response contract. This section covers
the security-relevant design decisions. The core guarantee:

> A share is a controlled, revocable authorization GRANT — never
> ownership transfer, never a second, independent access path that
> bypasses RBAC/ABAC/RLS. It is a second AUTHORIZATION PATH alongside
> case membership, evaluated by the exact same centralized
> `authz.Service.CanAccessDocument` every document route already calls.

```text
documents_select RLS (and CanAccessDocument's Go-side mirror) permits SELECT/access when:

    current_app_role() = 'ADMIN'
    OR (case member of the document's case)              <- the ORIGINAL authorization path
    OR has_active_document_share(document.id, caller.id)  <- the NEW, narrower path this system adds
```

### Authorization: one centralized check, two paths, never a third

`ShareService.CreateShare`/`ListShares`/`RevokeShare` all authorize via
`authz.Service.CanAccessDocument(user, documentID, authz.ActionDocumentShare)`
— the identical RBAC-AND-ABAC pattern verify/download/redact/certificate
already use. No new authorization engine, no hand-rolled role check.
`document:share` was already seeded (System 2/4) and already held by
POLICE/LAWYER/ADMIN per the existing role_permissions matrix — this
system reuses that grant rather than expanding it.

The genuinely new piece is `authz.Service.shareGrantsAccess`
(`internal/authz/share_policy.go`), consulted only AFTER RBAC passes and
ONLY once the ORIGINAL case-relationship check has already failed — a
second, narrower fallback, never a replacement:

```go
allowed, _ := HasPermission(user, action)     // RBAC — unchanged, checked FIRST
...
if rel.isOwner || rel.isMember { allow }       // ABAC path 1 — unchanged
delegated, _ := shareGrantsAccess(user, documentID, action)
if delegated { allow }                         // ABAC path 2 — NEW
deny
```

Because RBAC is checked first and is completely unaffected by sharing, a
share can only ever grant an action-TYPE the recipient's ROLE already
holds via RBAC — it only closes the "which SPECIFIC document" gap, never
the "which KIND of action" gap. A LAWYER (who holds no `document:verify`
permission at all, per the seed data) cannot verify a shared document
even with a `VERIFY`-tier share; a FORENSICS user (who does hold
`document:verify`) can, once shared with, verify a document outside
their case. This is a direct, tested consequence of reusing RBAC exactly
as-is (`TestShareService_DelegatedAccess_VerifyGrantsBoth` uses FORENSICS
for exactly this reason, not LAWYER).

### RLS: a second authorization path, and the recursion it caused

Master prompt guidance asked for RLS to permit access when "the user is
directly authorized OR has an active valid delegated access" — implemented
by adding an OR-branch to the EXISTING `documents_select` and
`compliance_certificates_select` policies (via `ALTER POLICY`, never a
DROP+recreate that could silently lose behavior).

The first implementation attempt inlined a raw
`EXISTS (SELECT 1 FROM document_shares ...)` into that branch — and
immediately hit PostgreSQL error 42P17, "infinite recursion detected in
policy for relation documents". The reason: `document_shares` carries its
OWN RLS policy (`document_shares_select`), which itself joins back into
`documents` (so a case member can see a document's share list). Evaluating
`documents_select`'s new branch therefore required evaluating
`document_shares_select`, which required re-evaluating `documents_select`
— an unbounded cycle PostgreSQL correctly refuses to run.

The fix: `has_active_document_share(document_id, user_id)`, a
`SECURITY DEFINER` SQL function owned by the migrator role (a superuser —
superusers are exempt from RLS unconditionally, `FORCE ROW LEVEL SECURITY`
notwithstanding). Calling it from `documents_select` queries
`document_shares` directly, without ever re-entering `document_shares`'s
own RLS, breaking the cycle. `document_shares_select` itself is
unaffected and still safely references `documents` in the other
direction (evaluating `documents_select`, which no longer loops back) —
see `db/migrations/000004_document_sharing.up.sql`'s inline comment at
the `CREATE FUNCTION` site for the full mechanical explanation, and
`TestMigration_UpDownUpIsReproducible` (`backend/tests/db_migration_test.go`)
for proof the schema still applies cleanly from scratch.

### Permission tiers: VIEW and VERIFY only, deliberately no DOWNLOAD

`document_shares.permission` is `VIEW` or `VERIFY` — not a third
`DOWNLOAD` tier some early drafts of this feature's spec suggested. This
application has no distinct "view metadata without downloading bytes"
capability (no inline document renderer exists — see
`document-viewer.component.ts`'s own "Inline preview is not available"
note), so `VIEW` already covers `document:read` + `document:download` +
`certificate:read` (a certificate is no more sensitive than the hash it
already contains — master prompt: "certificate access follows document
view permission"). `VERIFY` is a strict superset, additionally granting
`document:verify`. Neither tier — at any level — ever grants
`document:redact`, `document:share` (resharing), `certificate:create`, or
any write/delete action; this is enforced structurally in
`internal/authz/share_policy.go`'s `shareViewActions`/`shareVerifyActions`
maps (an action simply never appears in either map, rather than being
excluded by a runtime check that could be gotten wrong) and verified
directly by `TestShareService_DelegatedAccess_CannotRedactViaShare` and
`TestShareService_DelegatedAccess_CannotReshareViaShare` — the latter
using a `VERIFY`-tier share (the highest tier) specifically to prove even
the most privileged share still cannot reshare.

### Expiration and revocation: server-enforced, both layers

`expires_at` is optional (`NULL` = non-expiring) and, when present, must
be strictly in the future at creation time. Expiry is evaluated
server-side in TWO independent places that must agree: the SQL query
`GetActiveShareForDocumentAndUser`/`has_active_document_share` (both
filter on `expires_at IS NULL OR expires_at > now()`) and
`shareGrantsAccess`'s own Go-side re-check of the same condition on the
row it retrieves — belt-and-suspenders, not redundant decoration: neither
layer trusts that the other already got it right.

Revocation is a single, permanent `ACTIVE -> REVOKED` transition
(`ShareService.RevokeShare`, backed by
`UPDATE ... WHERE id = $1 AND document_id = $2 AND status = 'ACTIVE'`) —
never a DELETE (no DELETE grant/query exists on `document_shares`, exactly
like `redactions`/`compliance_certificates`), so the historical record of
who was granted what, by whom, and when, is permanent. There is
deliberately no "un-revoke": granting access again after revocation means
creating a brand-new share row, which is what an owner reasonably wants
anyway — an unbroken, auditable trail of "revoked, then a NEW grant was
made" rather than a single row silently flipping back and forth.

`TestShareService_DelegatedAccess_RevokedShareDeniesAccess` and
`...ExpiredShareDeniesAccess` prove both are enforced immediately and
server-side — a client cannot bypass either by simply not refreshing its
own UI state, since the NEXT request re-evaluates
`CanAccessDocument`/RLS from scratch every time (no session-cached
authorization decision anywhere in this codebase).

### IDOR protection

Every share-touching route is document-scoped and goes through
`RequireDocumentAccess`/`CanAccessDocument` exactly like every other
document route — a document the caller has no relationship to (real or
guessed ID) is denied with the SAME generic 403 as everywhere else in this
codebase, never a distinguishable response. `RevokeShare`'s share lookup
(`GetDocumentShareByID`) is additionally scoped to BOTH the share's own ID
AND the document ID in the URL: a real share ID that happens to belong to
a DIFFERENT document is treated identically to a nonexistent one (404),
so a caller cannot probe whether a given share ID exists at all by
supplying documents they merely guessed.
`TestShareService_RevokeShare_CrossDocumentShareIDDenied` and the
`document_share_flow_integration_test.go` HTTP-level IDOR block
(FORENSICS attempting to list/create/revoke shares on a document it has
no relationship to; POLICE attempting to revoke a real share through an
unrelated document ID) cover this directly.

### Recipient validation and enumeration resistance

`ShareService.validateRecipient` confirms the named recipient exists AND
is currently `active` — a single generic "Invalid or inactive recipient"
message covers BOTH failure reasons (the same non-enumerating posture
this codebase already applies to document/case IDOR responses, applied
here to user IDs). `GET /users/search` (the recipient picker's only data
source) requires authentication, a real query (minimum 2 characters —
never a bare listing), returns only a small safe field subset (no phone,
status, or timestamps), caps results at 10 regardless of match count,
excludes inactive users, and excludes the caller themself — deliberately
NOT gated behind the admin-only `user:read` permission (`GET /admin/users`
remains ADMIN-only global user management, a materially different,
more sensitive capability — see `UserService.ListUsers`'s own doc
comment), since any authenticated user legitimately needs to find a share
recipient.

### Deactivation

A recipient who is deactivated AFTER a share was created loses usable
access immediately, on their VERY NEXT request — not because this system
adds a new check, but because `internal/middleware.Auth` already
re-resolves the caller's CURRENT status from the database on every single
request (`AuthService.ResolveIdentity`, System 3), rejecting with a
generic 401 before any document/share-specific authorization even runs.
This system's own responsibility is narrower and already covered:
refusing to CREATE a share naming an inactive recipient in the first
place (see "Recipient validation" above) — the share record itself is
never deleted when a recipient is later deactivated, preserving the
historical grant for audit purposes; it simply becomes unusable exactly
like every other authenticated route already is for that account.

### Redacted-derivative sharing: lineage is never an authorization bypass

A share is created against ONE EXACT `document_id` — the original OR a
redacted derivative, never both. Sharing derivative `B` (from System 8)
never grants access to source `A`, and sharing `A` never automatically
shares `B`: `document_shares.document_id` names exactly one row, and
`CanAccessDocument`'s delegated-access check only ever looks up a share
for the SPECIFIC document ID being accessed — `documents.parent_document_id`
is never consulted by any authorization path in this system.
`TestShareService_RedactedDerivative_SharingDerivativeDoesNotGrantOriginal`
proves this directly: a recipient with an active share on the derivative
can download it, but the identical call against the original returns the
same 403 anyone with no relationship to that document gets.

### Document integrity is untouched

`ShareService` never imports `internal/storage`, never touches
`documents.sha256_hash`, and never calls anything that would (no
`Storage.Put`, no hash recomputation). A share is a
`document_shares` row and nothing else — sharing changes only access
metadata. `TestShareService_DocumentIntegrity_SharingDoesNotChangeHash`
uploads, records H1, shares, downloads and verifies as the recipient, and
asserts the verification's `stored_hash` is bit-for-bit H1 — proving
sharing created no new document version and rewrote no canonical hash.

### Audit

Every successful share creation records `DOCUMENT_SHARED` (document ID,
recipient ID, permission, whether it expires — never raw file content);
every revocation records `DOCUMENT_SHARE_REVOKED`. Both go through the
same `internal/audit.Recorder` interface every prior system uses. No
cryptographic audit-chain logic was introduced here — that remains a
separate, later system's job, exactly as every prior system already
established for its own events. Delegated download/verify access is
audited exactly like any other download/verify — `DocumentService`
records `DOCUMENT_DOWNLOADED`/`DOCUMENT_VERIFIED`/
`DOCUMENT_INTEGRITY_FAILURE` identically regardless of whether the
caller's access came from case membership or a share; there is
deliberately no separate "accessed via delegation" audit action, since the
share itself (queryable via `GET /documents/:id/shares`) is already the
durable record of who was granted what.

### What this system does *not* do

- **A new authorization engine** — `authz.Service.CanAccessDocument` is
  extended with one new fallback method
  (`shareGrantsAccess`); RBAC (`HasPermission`) is completely untouched.
- **Expanding who may share** — `document:share`'s existing
  role_permissions grants (POLICE, LAWYER, ADMIN, per the seed data) are
  reused unmodified; this system does not touch `role_permissions`.
- **Public/anonymous links, link-plus-password access** — sharing is
  strictly authenticated-user-to-authenticated-user; there is no token-
  based link anywhere in this system, and no route that skips
  `middleware.Auth`.
- **The audit hash chain** — unchanged.
- **An "act as user"/impersonation mechanism for ADMIN** — ADMIN's broad
  access (via `isAdmin` in `CanAccessDocument`) is, as always, attributed
  to ADMIN's own identity in every audit event; sharing adds no new
  admin capability.
- **Case-closure-aware share restrictions** — this codebase's existing
  case lifecycle (`cases.status`) does not gate document access for
  ANY existing route (upload, download, verify, redact) today; sharing
  does not invent a new restriction that would apply to it alone and
  nowhere else.
- **Rate limiting** — this codebase has no general rate-limiting
  infrastructure for ANY route yet (a future system's scope, per
  `TECH_STACK.md`'s "Not yet added" list); sharing does not add one
  either, consistent with reusing only what already exists.

## Cryptography

- **SHA-256** (`pkg/hash`) — document integrity hashing at upload
  (System 6) and recompute-and-compare verification (System 7,
  above). Streaming (`io.Copy`, never `io.ReadAll`), lowercase hex
  representation, verified against known test vectors
  (`backend/pkg/hash/sha256_test.go`).
- **ECDSA P-256** (`pkg/crypto.{GenerateECDSAKey,ParseECDSAPrivateKeyPEM,SignECDSA,VerifyECDSA}`,
  System 7) — compliance-certificate signing. ASN.1 DER signatures over a
  payload's SHA-256 digest (`crypto/ecdsa`'s standard wire format); keys
  are PEM-encoded PKCS#8, configured via `CERTIFICATE_SIGNING_KEY` (never
  hardcoded, never logged, never returned through an API response) with
  an ephemeral, process-lifetime fallback when unset — see "Document
  Verification & Compliance Certificates" above for the full design
  rationale.
- **AES-256 encryption at rest** and **RSA** signing remain unimplemented
  — no system through 7 needs either; `pkg/crypto/aes.go` remains a TODO
  stub.

## Transport Security

TODO: Document TLS configuration and certificate management.

## Threat Model

TODO: Document assumptions, trust boundaries, and mitigations relevant to
investigative/judicial evidence handling.
