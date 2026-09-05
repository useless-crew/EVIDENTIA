# Audit Chain

## Purpose

Evidentia's audit trail is a tamper-evident, chronological record of
security-sensitive actions: who did what, to which resource, when, and
what the previous audit entry was. It is System 8's contribution, built
entirely on top of `audit_log`'s storage invariants established by
System 2's schema migration (`backend/db/migrations/000001_init_schema.up.sql`)
— no new table, no second competing audit system.

It is explicitly **not** an application log. Debugging/diagnostic output
goes to the structured operational logger (`internal/logger`); the audit
trail is evidence-oriented, append-only, and cryptographically chained.
See [SECURITY.md](./SECURITY.md)'s "Implemented in System 8" for how this
integrates with every prior system's security-sensitive operations.

```text
Request
   |
Auth (internal/auth)
   |
RBAC / ABAC / RLS (internal/authz, PostgreSQL RLS)
   |
Business Operation (CaseService, DocumentService, AuthService, ...)
   |
audit.Recorder.Record(event)          <- the ONE integration point
   |
audit.ChainWriter.append
   |
PostgreSQL Transaction (repository.WithTx)
   |
pg_advisory_xact_lock(chain lock key)  <- serializes concurrent writers
   |
GetLatestAuditEntry (as an internal ADMIN-equivalent RLS identity)
   |
Canonicalize entry + compute SHA-256 (internal/audit.ComputeEntryHash)
   |
INSERT INTO audit_log (id, ..., prev_hash, hash)
   |
COMMIT                                  <- advisory lock released here
```

## Entry Structure

Each `audit_log` row (see `db/migrations/000001_init_schema.up.sql`) carries:

| Column | Type | Notes |
|---|---|---|
| `id` | `UUID` | External identifier, generated in Go *before* insert (needed as hash input) |
| `seq` | `BIGINT GENERATED ALWAYS AS IDENTITY` | Deterministic, gap-tolerant monotonic ordering for chain traversal — never trusted-timestamp ordering (clock skew, concurrent inserts) |
| `timestamp` | `TIMESTAMPTZ` | Generated in Go, truncated to microsecond precision (Postgres's own `timestamptz` resolution) so the hashed value and the value read back are bit-identical |
| `user_id` | `UUID`, nullable | The real actor; `NULL` only for a genuinely system-initiated event with no user (e.g. a login attempt against an email matching no account at all) |
| `role` | `TEXT`, nullable | The role the actor was acting as **at write time**, captured verbatim — never re-derived later, since a user's roles can change after the fact |
| `action` | `TEXT` | e.g. `DOCUMENT_UPLOADED` — see "Event Catalog" below |
| `resource_type` / `resource_id` | `TEXT` / `UUID`, nullable | Structured resource reference — `resource_id` is deliberately not a foreign key (multiple possible target tables; a polymorphic FK isn't expressible in Postgres), so referential integrity for it is this layer's concern, not the schema's |
| `case_id` | `UUID`, nullable | Case-scoping for RLS and filtering, independent of `resource_type`/`resource_id` |
| `metadata` | `JSONB` | Bounded, sanitized event detail — see "Metadata" below |
| `prev_hash` | `BYTEA(32)`, nullable | `NULL` only for the single genesis entry |
| `hash` | `BYTEA(32)` | This entry's own SHA-256 digest |

`id`/`timestamp` are supplied explicitly by `ChainWriter`, not left to
column `DEFAULT`s (`gen_random_uuid()`/`now()`): both are hash inputs, so
the writer must know their final values *before* the row is inserted —
you cannot hash a value you have not decided yet. `seq` is the one field
that genuinely cannot work this way (`GENERATED ALWAYS AS IDENTITY`
rejects an explicit value) and is deliberately **excluded** from the hash
input — chain *order* is independently verified by walking rows in `seq`
order and checking `prev_hash` linkage (see "Verification" below), so
`seq` is never treated as attacker-controlled input a forger could
otherwise substitute.

## Hash Construction

`internal/audit.ComputeEntryHash` is the **one** function in this codebase
that computes an audit entry hash. Both `ChainWriter` (on insert) and the
verifier (on verification) call this exact function — never a
re-implementation — so the two can never silently drift apart.

```text
entry_hash = SHA256(canonical_string(entry_without_its_own_hash))
```

where `canonical_string` is a fixed-field-order, labeled, newline-
delimited string (`internal/audit/chain.go`):

```text
evidentia-audit-entry
id=<uuid>
timestamp=<RFC3339Nano, UTC>
user_id=<uuid or empty>
role=<role>
action=<action>
resource_type=<resource_type>
resource_id=<uuid or empty>
case_id=<uuid or empty>
metadata=<canonical JSON>
prev_hash=<lowercase hex, or the literal "genesis">
```

This is deliberately **not** `json.Marshal` on a Go struct: struct memory
layout, map iteration order, and `encoding/json`'s field ordering on an
arbitrary type are not stable, portable contracts to hash over. A fixed,
hand-written field order with an explicit label per field is. `prev_hash`
is included as **one** of these fields — never appended a second time
after the string is built — so the entry never double-counts its own
predecessor or (impossible, since it doesn't exist yet at hash time)
includes its own hash in its own input.

**Genesis is a distinct literal token**, not merely "empty bytes": the
canonical string writes the literal `genesis` in place of a hex-encoded
`prev_hash` when `prev_hash` is `nil`. This matters because a `prev_hash`
that happens to be all-zero bytes must never hash identically to "no
`prev_hash` at all" — they are semantically different, and a test
(`TestComputeEntryHash_GenesisDistinctFromRealPrevHash`) confirms this.

Hash algorithm: **SHA-256** only (`crypto/sha256`, Go's standard library) —
never MD5, SHA-1, or any non-cryptographic hash. Hashes are stored as raw
`BYTEA(32)` in PostgreSQL (`CHECK (octet_length(hash) = 32)`) and are
**always** hex-encoded (lowercase) at the API/JSON boundary — the same
convention `pkg/hash.SumHex` already established for document hashes —
never raw bytes, never base64, never mixed representations.

### Metadata canonicalization

`audit_log.metadata` is `JSONB`. Postgres's `jsonb` storage does not
guarantee preserving the exact input byte layout (key order, whitespace)
of what was originally inserted — hashing "whatever the database happens
to hand back" would make verification depend on PostgreSQL's internal
`jsonb` text-output format rather than the actual content. The fix
(`internal/audit.CanonicalizeMetadata`): decode into a Go map and
re-encode via `encoding/json.Marshal`, which is documented to sort object
keys deterministically (recursively, for nested objects too), regardless
of the original input's key order or whitespace. This runs at **both**
insert time (before hashing/storing) and verify time (on whatever was
read back), so the two always agree — canonicalizing an already-canonical
value is idempotent. `nil`/empty metadata canonicalizes to `{}` (never the
JSON literal `null`), matching the same empty-metadata convention already
used elsewhere in this codebase (e.g. `DocumentService.UploadDocument`).

Metadata is deliberately **bounded and sanitized**, never a full request
body: each event records only what is necessary to explain it (e.g.
filename and size for an upload, `result_count`/`total` for an audit
list). It must never contain plaintext passwords, password hashes, access
tokens, refresh tokens, MinIO credentials, database credentials, or
signing keys — every existing `recorder.Record` call site across
Systems 3-7 was written (before System 8 existed at all) with this
constraint already in mind, and System 8 changes none of them.

## Genesis Entry

The chain has exactly one genesis entry: the row with `prev_hash IS
NULL`. This is enforced at the database level by a partial unique index
established in System 2's schema migration:

```sql
CREATE UNIQUE INDEX idx_audit_log_single_genesis
    ON audit_log((1)) WHERE prev_hash IS NULL;
```

A unique index on a constant expression, filtered to the rows in
question, is the standard Postgres idiom for "at most one" instead of
"one per distinct value" — a second `INSERT` attempting `prev_hash =
NULL` is rejected by the database itself, not merely discouraged by
application code. Symmetrically:

```sql
CREATE UNIQUE INDEX idx_audit_log_prev_hash_unique
    ON audit_log(prev_hash) WHERE prev_hash IS NOT NULL;
```

guarantees at most one entry may claim any given non-null predecessor —
"one canonical successor per entry", the database-level fork-prevention
invariant `ChainWriter`'s concurrency design (below) relies on as a
backstop.

This installation is single-tenant (one `audit_log` table, one chain) —
there is no per-agency/per-case audit chain isolation in this schema.
Case-scoped **visibility** into that one chain is RLS's job (see
"Row-Level Security" below), not a separate chain per case.

## Writing

`internal/audit.ChainWriter` (`internal/audit/writer.go`) is the
authoritative `audit.Recorder` implementation, wired into `app.New` in
place of System 3's original `audit.SlogRecorder` placeholder — **the
entire integration point**. Every existing `recorder.Record` call across
Systems 3-7 (auth, authorization denials, case, document, certificate,
redaction, share services) starts durably, tamper-evidently persisting
to `audit_log` the moment this type is constructed, with **no change** to
any call site: they all already depended on the `audit.Recorder`
interface, never a concrete type.

`Record` never returns an error (the interface's own contract, unchanged
since System 3): a login, upload, or any other operation must never fail
merely because audit recording had a problem. A write failure is logged
at `ERROR` level with full diagnostic detail — the one place an audit
write failure becomes visible at all — and otherwise silently absorbed.
This is a deliberate availability/fidelity tradeoff, not an oversight: see
"Failure Semantics" below for exactly which operations this decision
affects.

### Concurrency safety

Multiple requests recording audit events concurrently must never fork the
chain — two entries computing the same `prev_hash` from a stale read of
the "current" head. The authoritative guarantee lives at the database
transaction level, not in application-level locking (which would do
nothing across pooled connections or multiple backend processes):

```sql
-- name: AcquireAuditChainLock :exec
SELECT pg_advisory_xact_lock(sqlc.arg(lock_key)::bigint);
```

`ChainWriter.append` acquires this **transaction-scoped** advisory lock
(a fixed, arbitrary constant reserved solely for this purpose — it names
no row and touches no other lock table) *before* reading the current
chain head, and holds it for the whole transaction — automatically
released at `COMMIT`/`ROLLBACK`, never leaked across a pooled connection.
At most one transaction at a time can therefore be "between" reading the
head and inserting the entry that claims it as predecessor. The
`idx_audit_log_prev_hash_unique`/`idx_audit_log_single_genesis` unique
indexes are a pure defense-in-depth backstop on top of this — never the
primary mechanism — and a rare unique-violation conflict (SQLSTATE
`23505` on one of those constraints) triggers a bounded retry (3
attempts) against the now-current head rather than a permanent failure.

Verified by `TestChainWriter_ConcurrentWritesDoNotFork`
(`backend/internal/service/audit_service_integration_test.go`): 40
goroutines call `Record` simultaneously; afterward, every committed entry
forms one valid chain (exactly one genesis, every `prev_hash` claimed by
exactly one entry) — run under `go test -race`.

### Why the chain-head lookup uses its own internal RLS identity

`GetLatestAuditEntry` — the read `ChainWriter` uses to learn the true
`prev_hash` for its next entry — runs inside the same transaction as
every other `audit_log` query, which means it is subject to
`audit_log_select`'s RLS policy like any other `SELECT`. That policy
restricts a non-`ADMIN` identity to rows it owns (or a case it's an
active member of) — exactly the rows written by *other* actors would be
invisible to.

If `ChainWriter` read the head under the **acting user's own** identity
(the natural first instinct), the very first audit write by any actor who
didn't happen to already own the current chain-head row would see zero
visible rows, wrongly conclude "the chain is empty", and attempt a second
genesis entry — which the unique index above correctly rejects, but the
net effect is the legitimate event **silently fails to record** (logged,
per `Record`'s contract, but never persisted). This is exactly the RLS
interaction this design has to get right: RLS's job is bounding what a
request-scoped read exposes to its caller, not bounding what an internal
system component needs to see in order to maintain its own invariants.

The fix: `ChainWriter.append`'s internal transaction establishes an
**ADMIN-equivalent** `app.role` for its own `GetLatest` read (see
`chainWriterRLSRole` in `internal/audit/writer.go`), which lands on
`audit_log_select`'s own already-legitimate unrestricted-visibility
branch — the same branch a real ADMIN user's own request would use. This
is not an RLS bypass:

- No other RLS-protected table is ever touched inside this transaction
  — only `audit_log` (via `AcquireChainLock`, `GetLatest`, `Insert`).
- The row's own stored `role` and `user_id` columns are **unaffected** —
  they still record the real actor and the real role from `event.Role`/
  `event.UserID`, exactly as before.
- The `INSERT` policy (`audit_log_insert`) only requires
  `current_app_user_id() IS NOT NULL` — it does not care about role at
  all, so this has no bearing on insert authorization.

A dedicated sentinel UUID (`systemActorID`) is used as `app.user_id` only
for a genuinely userless event (e.g. a login attempt for an email that
matches no account) — never written into `audit_log.user_id` itself,
which stores the event's real `UserID`, left `NULL` when there is none.

## Verification

`POST /audit/verify-chain` (`internal/service.AuditService.VerifyChain`,
ADMIN-only via `audit:verify`) walks the entire chain from a starting
`seq` (default `0` — genesis) in bounded-size batches, recomputing and
comparing each entry's hash and `prev_hash` linkage:

```text
for each batch of up to 1000 rows (defaultVerifyBatchSize), in ascending seq order:
    for each row:
        row.prev_hash == expected_prev_hash?         no -> INTEGRITY_FAILURE
        ComputeEntryHash(row) == row.hash?            no -> INTEGRITY_FAILURE
        expected_prev_hash := row.hash
    if fewer rows than the batch size came back: end of chain reached
```

`internal/audit.VerifyBatch` is pure and side-effect-free (no database
access) — `AuditService.VerifyChain` supplies successive pages and
threads `LastHash` from one call into the next call's expected
`prev_hash`, so a chain of any size verifies in **bounded memory**: this
function never needs to see more than one page's worth of rows at once.

**Large chains never load fully into RAM.** A single HTTP call processes
up to `maxVerifyEntriesPerRequest` (200,000) entries before returning; if
more remain, the response carries `next_seq` and the caller resumes with
`from_seq=next_seq` — a resumable, batched design compatible with a
future progress-reporting UI (explicitly out of scope for this system —
see "Relationship to Async Work" below) without requiring one now.

The response is always a `200` carrying `status`:

- `VERIFIED` — the entire requested range checks out. An **empty** chain
  is vacuously `VERIFIED` (zero entries checked, no genesis yet).
- `INTEGRITY_FAILURE` — the first entry that fails, with enough safe
  diagnostic detail to locate it: `failed_entry_id`, `failed_seq`,
  `expected_prev_hash`/`actual_prev_hash` (hex), `expected_hash`/
  `actual_hash` (hex), and a human-readable `reason`. **Never** metadata
  content or any other potentially sensitive payload — only identifiers
  and hashes.

Both outcomes are a successful verification *answering the question*,
never an HTTP error — the same "VERIFIED/INTEGRITY_FAILURE, both a 200"
posture System 7 already established for document-hash verification
(`VerificationResult`).

### What verification detects

Every case in the mandatory tamper matrix is covered by tests
(`backend/internal/audit/verifier_test.go`,
`backend/internal/service/audit_service_integration_test.go`):

- Modifying `action`, `user_id`, `role`, `timestamp`, `resource_type`,
  `resource_id`, `case_id`, or `metadata` on any committed entry —
  `entry_hash` recomputation no longer matches the stored `hash`.
- Modifying `prev_hash` or `hash` directly.
- **Deleting** a committed entry — the next remaining entry's `prev_hash`
  no longer matches anything in the (now-shorter) sequence.
- **Inserting a forged/forked entry** claiming an existing entry's hash
  as its own predecessor — detected as either a broken link at the
  position that expected the *real* successor, or (at the database level,
  independent of Go verification entirely) rejected outright by
  `idx_audit_log_prev_hash_unique` before it can even commit.
- Genesis corruption (a non-`NULL` `prev_hash` on what should be the
  first entry, or more than one `NULL`-`prev_hash` row) — caught by
  `VerifyBatch`'s own check and, independently, by the database-level
  unique index that would have refused the second genesis at insert time.

`TestAuditService_VerifyChain_DetectsTamperingViaPrivilegedConnection`
demonstrates this against a **privileged** connection (the migrator role,
a Postgres superuser, `RLS`-exempt) directly modifying a committed row —
proving the cryptographic chain is a genuine *second* layer of defense,
not merely decorative given that `evidentia_app` itself already cannot
`UPDATE`/`DELETE` `audit_log` at all (see "Append-Only Database
Permissions" below). This does not weaken production permissions; it uses
a deliberately different, separately-privileged connection to simulate an
attacker (or operator error) who somehow obtained elevated database
access.

## Append-Only Database Permissions

`evidentia_app` (the runtime role `cmd/server` connects as) holds exactly:

```sql
GRANT SELECT, INSERT ON audit_log TO evidentia_app;
REVOKE UPDATE, DELETE ON audit_log FROM evidentia_app;
```

No `UPDATE`, no `DELETE`, at the **database** level — not merely an
application-code convention. `evidentia_app` also does not own the table
(a privileged migrator role does — an owner can `ALTER`/`DROP` regardless
of `GRANT`s), is not a Postgres superuser, and holds no `BYPASSRLS`.
Verified end-to-end by `backend/tests/db_audit_privileges_test.go`:

- `TestAuditPrivileges_RuntimeRoleCannotUpdateOrDelete` — a direct
  `UPDATE`/`DELETE` from `evidentia_app` fails with `permission denied`.
- `TestAuditPrivileges_RuntimeRoleDoesNotOwnAuditLog`
- `TestAuditPrivileges_RuntimeRoleIsNotSuperuserAndDoesNotBypassRLS`
- `TestAuditPrivileges_RuntimeRoleCanSelectAndInsertOnly` — the exact
  privilege set is `{SELECT, INSERT}`, nothing more.
- `TestAuditPrivileges_GenesisEntryMustBeUnique` /
  `TestAuditPrivileges_OnePredecessorPerEntry` — the unique indexes
  themselves, independent of any Go hashing logic.

## Row-Level Security

`audit_log` has RLS enabled and `FORCE`d (System 2's migration — even the
table owner is subject to it):

```sql
CREATE POLICY audit_log_select ON audit_log FOR SELECT
    USING (
        current_app_role() = 'ADMIN'
        OR user_id = current_app_user_id()
        OR (case_id IS NOT NULL AND EXISTS (
            SELECT 1 FROM case_members cm
            WHERE cm.case_id = audit_log.case_id
              AND cm.user_id = current_app_user_id()
              AND cm.removed_at IS NULL
        ))
    );

CREATE POLICY audit_log_insert ON audit_log FOR INSERT
    WITH CHECK (current_app_user_id() IS NOT NULL);
```

No `UPDATE`/`DELETE` policy exists at all — combined with the grants
above, this is append-only enforcement at two independent layers (RLS
*and* table privileges), neither one alone sufficient by itself. Missing
`app.user_id`/`app.role` context (an unauthenticated or misconfigured
transaction) means **zero visible rows and a rejected insert** — RLS
fails closed, matching every other RLS-protected table in this schema.

`GET /audit` (`internal/service.AuditService.List`) runs its query under
the **caller's own** identity — deliberately the opposite of
`ChainWriter`'s internal ADMIN-equivalent read above, because here the
row-level restriction *is* the intended behavior: ADMIN sees every entry;
every other role sees only its own actions plus entries tied to a case it
is an active member of. A query filter (`user_id`, `action`,
`resource_type`, `resource_id`, `case_id`, date range) can only **narrow**
what RLS already permits, never widen it — a LAWYER supplying an
arbitrary/unrelated `user_id` filter simply gets zero rows, never another
user's history (verified against IDOR in
`TestAuditFlow_EndToEnd`/`TestAuditService_List_NonAdminSeesOnlyOwnActions`).

`POST /audit/verify-chain` is ADMIN-only (`audit:verify`, granted only to
ADMIN per the seed data) precisely because verifying "the chain" only
makes sense against the complete, unfiltered sequence — ADMIN's own
`audit_log_select` branch already returns every row unrestricted, exactly
the view chain verification needs.

## RBAC

Two permissions gate the audit trail (`backend/db/seed/001_reference_data.sql`):

| Permission | Granted to |
|---|---|
| `audit:read` | ADMIN, POLICE, LAWYER, JUDGE |
| `audit:verify` | ADMIN only |

`internal/authz.ActionAuditRead`/`ActionAuditVerify` are the typed Go
constants mirroring these exact permission names — checked via
`internal/authz.Service.HasPermission`, the same centralized RBAC
decision point every other resource in this codebase uses, both at the
route level (`middleware.RequirePermission`) and again, independently,
inside `AuditService` itself.

## Avoiding recursive audit-access events

`AuditService.List` records its own `AUDIT_ACCESSED` event — but only
*after* the underlying query has already run, and `audit.Recorder.Record`
only ever inserts one row; it never calls back into `AuditService` or
anything else. There is therefore no code path by which retrieving audit
data could trigger a further audit-access event, structurally, not via a
runtime recursion-depth guard that could itself be a bug.
`AuditService.VerifyChain` similarly records exactly one
`AUDIT_CHAIN_VERIFICATION_REQUESTED` event per call, after verification
completes.

## Event Catalog

Every action name currently written by the codebase (grep
`internal/audit.Recorder` call sites for the authoritative, current list —
this table is a snapshot):

| Category | Actions |
|---|---|
| Authentication | `AUTH_LOGIN_SUCCESS`, `AUTH_LOGIN_FAILED`, `AUTH_REFRESH_SUCCESS`, `AUTH_REFRESH_FAILED`, `AUTH_REFRESH_REUSE_DETECTED`, `AUTH_LOGOUT` |
| Authorization | `AUTHZ_DENIED` (every RBAC/ABAC denial) |
| Case | `CASE_CREATED`, `CASE_UPDATED` |
| Document | `DOCUMENT_UPLOADED`, `DOCUMENT_DOWNLOADED`, `DOCUMENT_VERIFIED`, `DOCUMENT_INTEGRITY_FAILURE`, `DOCUMENT_REDACTED`, `DOCUMENT_SHARED`, `DOCUMENT_SHARE_REVOKED` |
| Certificate | `CERTIFICATE_CREATED` |
| Admin / user management | user create/update/role-change/(de)activate events — see `internal/service/user_service.go` |
| Audit | `AUDIT_ACCESSED`, `AUDIT_CHAIN_VERIFICATION_REQUESTED` |

New security-sensitive operations should call the existing
`app.App.AuditService`'s underlying `audit.Recorder` (constructed once in
`app.New`, shared with every other service) rather than introducing a
second recording path — see "Audit Insert Authority" below.

## Audit Insert Authority

No HTTP handler constructs an audit record directly. Every event is
recorded through the shared `audit.Recorder.Record(event)` call inside a
**service**, never a handler, and every field on `audit.Event` is derived
from trusted, server-side context:

- `UserID`/`Role` — the authenticated caller (`auth.AuthenticatedUser`,
  resolved fresh from the database on every request), never a
  client-supplied header or body field.
- `Action`/`ResourceType` — a fixed string literal in the calling service
  code, never client input.
- `ResourceID`/`CaseID` — the server-side resource the operation actually
  touched (e.g. the case ID `CaseService.CreateCase` itself just created),
  never an arbitrary client-supplied ID trusted at face value.
- `Metadata` — a small, explicitly-constructed map of safe fields, never
  the raw request body.

A client can never submit `user_id: "someone-else"`, `role: "ADMIN"`,
`entry_hash: "..."`, or `previous_hash: "..."` and have those values
trusted — `audit.Event` has no such fields exposed to any handler in the
first place, and `hash`/`prev_hash` are computed entirely inside
`ChainWriter.append`, never accepted as input from anywhere.

## Failure Semantics

`Recorder.Record` never returns an error — established by System 3, kept
by System 8's real implementation. A login, upload, case creation, etc.
must never fail merely because audit recording had a transient problem
(database unavailable, an exhausted 3-attempt retry against a chain
conflict). This is a deliberate availability/fidelity tradeoff: an audit
write failure is logged operationally at `ERROR` level (the only place it
becomes visible) and otherwise silently absorbed — the business operation
itself still succeeds and is reported to the caller as such.

This is distinct from — and does not contradict — the "operation and its
audit event should not be split by a crash mid-transaction" concern:
`ChainWriter.append` runs entirely inside its own single transaction
(lock, read, hash, insert, commit); a cancelled context or database error
at any point before `COMMIT` rolls back the **entire** transaction,
leaving no partial row and no advanced chain head
(`TestChainWriter_CancelledContextDoesNotLeavePartialEntry` confirms
this). What is *not* attempted in this system is wrapping the **business**
operation's own transaction and the audit write into one single database
transaction — `Record` is called as a discrete step (typically after the
business operation's own transaction has already committed), consistent
with how every prior system since System 3 already called
`audit.Recorder`. The tradeoff this implies: in the narrow window where
the business operation committed but the subsequent `Record` call fails
every retry, that operation's audit trail entry for that one event is
missing, logged as an operational error, and does not roll back the
already-committed business change. No system in this codebase currently
wraps an external operation (e.g. a MinIO write) and its audit event in
one atomic unit either, for the same underlying reason: MinIO is not
transactional with PostgreSQL, so "the operation and its audit record
commit together" can only ever be approximated, never made truly atomic,
across two separate systems.

## Legacy Audit Data

There is no pre-hash-chain audit data to migrate. `audit_log`'s schema —
including `hash`/`prev_hash`, the length `CHECK` constraints, and both
partial unique indexes — was established from System 2's very first
schema migration, before any system began writing real rows to the table.
Every row ever written to `audit_log` in any environment running this
migration has therefore always been subject to genesis/chain-linkage
invariants from the first insert onward; there is no unchained legacy
data requiring a backfill or an explicit legacy marker.

## Asynchronous Verification & Integrity Dashboard (System 11)

System 10 (above) built the cryptographic chain and a synchronous,
single-HTTP-call `VerifyChain`. System 11 replaces that HTTP contract with
an asynchronous job so a chain of any size — 100,000, 1,000,000, or more
entries — never has to be checked within one request's lifetime, and adds
the operational surface (status tracking, SSE progress, history,
dashboard) to observe it. **It reuses System 10's verification logic
completely and unchanged**: `internal/audit.VerifyBatch`/
`ComputeEntryHash`/`CanonicalizeMetadata` are called by the new job
exactly as `VerifyChain` called them — there is still, and only ever,
**one** hash/canonicalization/chain-traversal implementation in this
codebase.

```text
Frontend                    (dashboard: signals-based state in
   |                         audit-verification.service.ts)
   | POST /audit/verify-chain
   v
Handler (internal/handlers/audit) -- AuditService.StartVerification
   |                                      |
   |                              INSERT audit_verifications (QUEUED)
   |                              [dedup: idx_audit_verifications_single_active]
   |                                      |
   |                              jobs.Client.EnqueueVerifyAuditChain
   v                                      |
202 Accepted {verification_id}            v
                                     Redis (Asynq queue — transport only)
                                           |
                                           v
                            jobs.AuditVerificationHandler.ProcessTask
                            (embedded worker, same process — cmd/server)
                                           |
                                 AuditService.RunVerification
                                           |
                              +----------------------------+
                              | for each batch (1000 rows): |
                              |   ListAuditEntriesFromSeq    |  <- System 10's own
                              |   audit.VerifyBatch          |  <- exact functions,
                              |   UPDATE progress (Postgres) |     unchanged
                              |   Broadcaster.Publish(event) |
                              +----------------------------+
                                           |
                              CompleteAuditVerification (Postgres)
                                           |
                                 realtime.Broadcaster
                                           |
                                           v
                      GET /audit/verify-chain/:id/events (SSE)
                                           |
                                           v
                                     Dashboard progress bar
```

### Job architecture

> **System 12 note:** `internal/jobs` is now Evidentia's general-purpose
> background-job infrastructure, not audit-verification-specific code —
> see [docs/BACKGROUND_JOBS.md](./BACKGROUND_JOBS.md) for the full
> architecture (queue priority, retry classification, structured logging,
> job IDs). `AUDIT_CHAIN_VERIFY` is that infrastructure's first and, so
> far, only task type, refactored onto it with **no change** to any of
> the behavior this section describes below. One addition: `POST
> /audit/verify-chain`/`GET /audit/verify-chain/:id` now also return
> `job_id` — a deterministic, traceable Asynq task ID
> (`jobs.AuditVerifyChainJobID`) derived from `verification_id` alone,
> which also makes a duplicate enqueue for the same verification
> impossible at the Asynq layer itself (`asynq.ErrTaskIDConflict`),
> underneath (never instead of) the database-level
> `idx_audit_verifications_single_active` dedup described below.

`internal/jobs` wraps `github.com/hibiken/asynq` (Redis-backed task queue —
already on the approved stack per TECH_STACK.md, unused until this
system). `VerifyAuditChainPayload` carries **only** a `verification_id`
(`internal/jobs/audit_verification.go`) — the worker loads every other
fact (which entries to check, what "correct" looks like) fresh from
PostgreSQL itself, exactly like `audit.Event` lets no client-supplied
field influence a hash; a client can never smuggle an expected hash,
canonicalization rule, chain head, or verification result through this
payload. It now runs on `jobs.QueueCritical` — System 12's highest-
priority queue, reserved for security-critical work — rather than a
plain, unnamed default queue.

The worker (`asynq.Server` + `asynq.ServeMux`) runs **embedded in the
same process/binary as the HTTP server** (`cmd/server/main.go`), not a
separate deployment unit — this project's docker-compose has no `worker`
service, and audit verification's workload (a handful of sequential
batched reads against the same PostgreSQL pool the HTTP server already
shares) does not justify the operational cost of a second container. A
future system with a genuinely different scaling profile can introduce
`cmd/worker` later without any change to `internal/jobs`/`internal/
service` — `jobs.NewServer`/`NewMux` take no dependency on `httpserver`.

`internal/jobs.AuditVerifier`/`AuditFailureRecorder` are narrow interfaces
`internal/jobs` defines and `*service.AuditService` satisfies
structurally (Go's implicit interface satisfaction) — `internal/jobs`
never imports `internal/service`, only the reverse (`AuditService` imports
`internal/jobs.Client` to enqueue). This is what lets `AuditService`
depend on the job-enqueueing client while the job **handler** calls back
into `AuditService.RunVerification` without an import cycle.

### Verification status model & lifecycle

A dedicated table, `audit_verifications` (migration `000005`), is the
**authoritative, durable** record — not Redis, which this system uses
purely as Asynq's queue transport and nowhere else. Lifecycle:

```text
QUEUED -> RUNNING -> VERIFIED
                   -> INTEGRITY_FAILURE
                   -> FAILED
```

- **QUEUED**: the row exists; the task has been enqueued; no worker has
  picked it up yet.
- **RUNNING**: a worker claimed it (`MarkAuditVerificationRunning`'s
  `WHERE status = 'QUEUED'` guard — see "Concurrency & idempotency"
  below) and is actively verifying.
- **VERIFIED**: the run completed and found no problem.
- **INTEGRITY_FAILURE**: the run completed and found a definite
  cryptographic/structural problem — see "Failure classification" below.
  This is a **successful, meaningful result**, exactly like System 10's
  `VerifyChain` treated it — never confused with an error.
- **FAILED**: the run could **not** complete due to an operational
  problem (PostgreSQL unavailable, an unexpected driver error, a timeout).
  **Never** used interchangeably with `INTEGRITY_FAILURE` — an outage is
  not evidence of tampering, and a genuine tamper finding is not a
  transient error to retry away.

Persisted fields (see the migration's own column comments for the full
detail): `entries_checked`, `total_entries` (captured once, at the
`RUNNING` transition, from a single `COUNT(*)` — never re-queried per
batch), `last_seq_checked` (the live progress cursor), `failed_entry_id`/
`failed_seq`/`failure_type`/`failure_reason` (INTEGRITY_FAILURE/FAILED
only — enforced by `audit_verifications_failure_fields_check`),
`requested_by_user_id`/`requested_by_role`, `started_at`/`completed_at`,
`created_at`/`updated_at`.

### Progress calculation

`progress_percent = entries_checked / total_entries * 100`, computed in
`internal/service.toVerificationDetail` from the two persisted counters —
never a separately-stored percentage that could drift from them.
`total_entries` is `NULL` only in the brief window before a `QUEUED` job
has been picked up; the API omits `progress_percent` in that case (an
explicit indeterminate state — "queued, not yet known how much work
remains" — rather than a misleading `0%`).

Progress is **throttled to once per batch** (`defaultVerifyBatchSize` =
1000 entries) — `AuditService.verifyBatches` persists a Postgres `UPDATE`
and calls `Broadcaster.Publish` exactly once per batch, never once per
row. For a 1,000,000-row chain this is ~1,000 database writes and ~1,000
SSE events for the entire run, not a stream of per-row events.

### Batching & large-chain strategy

`verifyBatches` reuses `AuditRepo.ListFromSeq` — the identical `WHERE seq
> $1 ORDER BY seq LIMIT $2` keyset-paginated query System 10's synchronous
verifier already used, never `OFFSET`-based paging (which degrades
quadratically for a very large table) and never `SELECT *` loaded into
memory at once. Each batch is its own short-lived transaction — the
function never holds one PostgreSQL transaction open across an entire
(potentially long) run, so a large chain's verification never holds locks
or a connection for its whole duration, and never keeps a transaction
open while waiting on anything SSE-related (the worker and the SSE
connection are fully decoupled — see "SSE architecture" below). The only
non-batched read is the single `COUNT(*)` for `total_entries`, done once
at job start.

### Failure classification

`internal/audit.BatchResult` (System 10) gained one additive field,
`FailureType`, populated by the SAME `VerifyBatch` function with no
change to its existing behavior or return values otherwise:

- `GENESIS_INVALID` — the first entry checked claims a predecessor, or an
  unexpected second genesis-shaped entry appears mid-chain.
- `PREVIOUS_HASH_MISMATCH` — any other broken link. This single category
  also covers a **deleted** entry (the next surviving entry's `prev_hash`
  no longer matches anything) and an **attempted** fork — see below for
  why those are not separate categories.
- `ENTRY_HASH_MISMATCH` — the recomputed SHA-256 doesn't match the stored
  hash.
- `CANONICALIZATION_ERROR` — stored metadata could not be canonicalized
  (theoretical in practice: PostgreSQL's `jsonb` column type itself
  guarantees syntactically valid JSON, so this path is defensive, not
  reachable via any realistic database-level tamper).

Master prompt's `CHAIN_FORK_DETECTED`/`DUPLICATE_ENTRY`/
`CHAIN_ORDER_INVALID`/`MISSING_ENTRY` are **not** implemented as separate,
distinguishable categories — not an oversight, a deliberate scope
decision: `idx_audit_log_prev_hash_unique`/`idx_audit_log_single_genesis`
(System 2) reject a forking or duplicate-genesis `INSERT` at the database
level *before* it can ever commit, so a fork or duplicate genesis
structurally cannot exist in a chain the verifier scans in the first
place — there is nothing to detect because it was already prevented.
`audit_log.seq` is a `GENERATED ALWAYS AS IDENTITY` column (monotonic by
construction), so there is no "chain order" for `ORDER BY seq` to get
wrong. A deleted entry is real and detectable, but is honestly reported as
what it structurally IS — a broken link — rather than a separate label
the algorithm cannot actually distinguish from other causes of the same
symptom.

`FAILED` uses a **different** vocabulary
(`internal/audit.OperationalFailure*`): `DATABASE_ERROR`, `TIMEOUT`, and
`STALE_TIMEOUT` (see "Stale verification recovery" below) — stored in the
same `failure_type` column, whose meaning depends on `status`.

### Concurrency & idempotency

- **Duplicate active jobs**: `idx_audit_verifications_single_active` (a
  unique index on a constant expression filtered to `status IN ('QUEUED',
  'RUNNING')` — the exact same idiom `idx_audit_log_single_genesis`
  already established) guarantees at the database level that at most one
  verification is ever `QUEUED`/`RUNNING` at a time.
  `AuditService.StartVerification` attempts an `INSERT`; on a 23505
  conflict against that specific index, it reads back and returns the
  **already-active** run's id instead of erroring or starting a second
  concurrent full-chain scan. Verified by
  `TestAuditService_StartVerification_DeduplicatesActiveRun` (20
  concurrent callers, exactly one row created, every caller receives the
  same id).
- **Duplicate/redelivered tasks**: `MarkAuditVerificationRunning`'s `WHERE
  status = 'QUEUED'` guard means a second attempt to start an
  already-`RUNNING` (or terminal) job matches zero rows; `RunVerification`
  treats that as "someone else already handles this" and returns `nil`
  immediately — no re-verification, no double-counted `entries_checked`.
  Verified by `TestAuditService_RunVerification_ConcurrentInvocationsDoNotCorruptState`
  (10 concurrent `RunVerification` calls on the same id; exactly one
  performs the real work).
- **Verification is read-only against the chain, always.** No code path
  in `RunVerification`/`verifyBatches`/`completeVerification` ever
  `UPDATE`s or `DELETE`s an `audit_log` row, ever "repairs" a hash, or
  reorders anything — a corrupted entry is reported (`INTEGRITY_FAILURE`),
  never fixed. `evidentia_app`'s own database grants make this
  structurally true regardless of application logic (see "Append-Only
  Database Permissions" above), and `audit_verifications` itself has no
  purchase over `audit_log` — inserting/updating a verification row never
  touches the chain table at all.

### Retry semantics

Asynq retries a task automatically when `ProcessTask` returns a non-nil
error (`asynq.MaxRetry(3)`, `asynq.Timeout(30 * time.Minute)` — see
`NewVerifyAuditChainTask`). `RunVerification` returns an error **only**
for a genuine operational failure (a database read/write failing, or
`ctx.Done()` from a timeout); it returns `nil` for both `VERIFIED` and
`INTEGRITY_FAILURE` — both are a *completed* run from Asynq's point of
view, so neither is ever retried. A malformed task payload wraps
`asynq.SkipRetry` (never retryable — no amount of retrying fixes a bad
payload).

A verification row is marked terminally `FAILED` **only after Asynq's
retry budget is exhausted** — `jobs.NewAuditVerificationErrorHandler`
(registered as the `asynq.Server`'s `ErrorHandler`) checks
`asynq.GetRetryCount`/`GetMaxRetry` on every failed attempt and calls
`AuditService.MarkVerificationOperationallyFailed` only when
`retried >= maxRetry` — an intermediate attempt that will still be
retried never marks the row `FAILED`, so a transient blip that succeeds
on retry 2 never leaves a stray `FAILED` row alongside the eventual real
`VERIFIED`/`INTEGRITY_FAILURE` outcome.

### Stale verification recovery

A worker can die outright (process killed, not merely slow) without ever
reaching its own completion or `MarkVerificationOperationallyFailed`
handling, which would otherwise leave a row `RUNNING` (or `QUEUED`)
forever. Rather than a separate scheduled sweeper/cron process,
`AuditService.reconcileStale` self-heals **lazily, at read time**: every
`GetVerification`/`ListVerifications`/`GetIntegritySummary` call (and
therefore every REST poll and every SSE connection's initial snapshot)
checks whether a `QUEUED`/`RUNNING` row's `updated_at` is older than a
threshold (5 minutes for `QUEUED` — no worker has even started it;
2 minutes for `RUNNING` — no progress reported) and, if so, **persists**
(not merely returns) a `FAILED`/`STALE_TIMEOUT` correction before
returning it. This guarantees master prompt's "a failed job must not
remain RUNNING forever" and "never incorrectly mark an interrupted
verification as VERIFIED" without a second background process: the
correction is applied the first time anyone looks, which is sufficient
since `audit_verifications` has no other consumer.

### SSE architecture

`internal/realtime.Broadcaster` is a small in-process pub/sub keyed by
`verification_id` — **not** Redis pub/sub. Because the worker and the
HTTP server share one process (see "Job architecture" above), there is no
cross-process coordination problem to solve; Redis's entire role in this
system stays "Asynq's queue transport", never a second, competing state
store for progress (master prompt: "do not allow Redis state to override
PostgreSQL's authoritative verification result"). `Broadcaster.Publish`
**never blocks**, even on a slow or absent subscriber (a bounded,
non-blocking buffered send — see that type's own doc comment) — this is
what keeps the verification worker and an SSE connection fully decoupled:
a stalled browser tab can never stall verification progress, and the
worker never touches a database transaction while waiting on anything
SSE-related.

`GET /audit/verify-chain/:id/events` (`internal/handlers/audit/events.go`)
performs the **exact same** `AuditService.GetVerification` authorization
check every REST caller goes through — `verification_id` in the URL is
never trusted as proof of authorization by itself — sends that already-
authorized snapshot immediately as the first SSE frame (so a reconnecting
client is never left waiting on the next event to learn current state),
then relays further events from the broadcaster until a terminal one is
sent or the client disconnects (`c.Request.Context().Done()`), at which
point `unsubscribe()` runs (deferred), releasing the broadcaster
subscription — no leaked goroutines or channels. A 15-second heartbeat
comment line (`: heartbeat\n\n`) keeps intermediate proxies from timing
out an idle connection between real events.

The handler subscribes to the broadcaster **before** running that
authorization check, not after — `Broadcaster.Subscribe` only registers an
in-memory channel and sends the caller no data by itself, so doing it
first discloses nothing. Doing it in the other order (read-then-subscribe)
loses events for a verification that reaches its terminal state — and
therefore publishes its one and only completion event — in the gap
between the authorization read and the `Subscribe` call: a real race for a
small/fast chain, where a background verification can complete in well
under the time the authorization DB round trip itself takes, permanently
losing the only completion event and leaving the connection open until
the client's own timeout. Subscribing first guarantees a concurrent
completion is always either already reflected in the authorization read
(an already-terminal initial event) or captured by the channel (delivered
as a normal subsequent event) — never both missed.

**Reconnection**: the frontend's SSE client (`audit-verification.
service.ts`) never treats "connection dropped" as final — on any
stream error it re-fetches the plain REST status endpoint
(`GET /audit/verify-chain/:id`) as the source of truth for current state,
then reopens the SSE connection if the verification is still non-
terminal. It never relies exclusively on having received every SSE event.

**Authentication**: the browser cannot attach an `Authorization` header
to a native `EventSource` connection, so the frontend deliberately uses
`fetch()` with a normal `Authorization: Bearer <token>` header and reads
the response body as a stream (manually parsing `event:`/`data:` frames),
rather than `EventSource` — this keeps the SSE route authenticated
identically to every other route (the same interceptor-attached bearer
token, the same 401-triggers-one-refresh-and-retry logic — see "Frontend
Integration" below), and never leaks a JWT into a URL (server/proxy
access logs, browser history) the way a `?access_token=...` query
parameter would.

### Security review (System 11-specific)

1. Can an unauthorized user start or inspect verification? No — every
   route (`POST /verify-chain`, `GET /verify-chain/:id`, the SSE route,
   `GET /verifications`, `GET /integrity`) requires `audit:verify`
   (ADMIN-only per the seed data), re-checked independently inside
   `AuditService`, with `audit_verifications`' own RLS
   (`current_app_role() = 'ADMIN'`) as a second, database-level layer.
2. Can a user access another verification by guessing/enumerating an ID?
   No — `audit_verifications_select`'s RLS restricts every non-ADMIN
   identity to zero rows regardless of the ID queried; an ADMIN can see
   every run, which is correct (the chain is a single global resource,
   not per-user/per-case data).
3. Can the browser fake `VERIFIED` or fake progress? No — the frontend
   renders exactly what `GetVerification`/the SSE stream return; nothing
   in the dashboard computes a hash, a percentage from anything but the
   server's own counters, or a status from anything but the server's
   response.
4. Can verification modify audit data or "repair" a corrupted entry? No —
   see "Concurrency & idempotency" above; verification is structurally
   read-only against `audit_log`.
5. Can Redis override the authoritative result? No — Redis holds no
   verification state at all in this design; PostgreSQL is the only
   store `AuditService` ever reads a verification's status from.
6. Can an SSE connection leak data across verifications or users? No —
   `Broadcaster.Subscribe`/`Publish` are keyed by `verification_id`, and a
   subscriber is only ever created after the SAME per-caller authorization
   check every REST route applies.
7. Can starting/completing verification create recursive or unbounded
   audit events? No — exactly two `audit.Recorder.Record` calls exist in
   the entire System 11 code path (`AUDIT_CHAIN_VERIFICATION_REQUESTED` at
   start, `AUDIT_CHAIN_VERIFICATION_COMPLETED` at the end of one run) —
   never one per batch, never one per entry checked.
8. Can a failed job remain `RUNNING` forever? No — see "Stale verification
   recovery" above.
9. Can multiple workers/duplicate task deliveries corrupt verification
   state? No — see "Concurrency & idempotency" above.
10. Can a huge audit table exhaust memory? No — see "Batching &
    large-chain strategy" above.
11. Can secrets enter a verification record? No — `audit_verifications`
    stores only identifiers, counts, a classified `failure_type`, and a
    hand-written, safe `failure_reason` string; it never stores a raw
    driver/SQL error, a stack trace, or a filesystem path.
12. Can RLS be bypassed? No — `evidentia_app` holds no `BYPASSRLS`
    (unchanged from System 10), and the worker's own internal identity
    (`workerIdentity` in `internal/service/audit_service.go`) still
    satisfies `audit_verifications`'/`audit_log`'s policies through their
    own ADMIN-equivalent branch, never by disabling RLS.

### Frontend integration

`frontend/src/app/core/services/audit-verification.service.ts` is the one
Angular service for this feature (following this codebase's existing
per-domain-service convention — `case.service.ts`, `document.service.ts`,
etc. — there is no TanStack Query in this Angular project; the equivalent
pattern here is a signals-based service wrapping `ApiClientService`, the
one central HTTP client every backend call already goes through). It
exposes:

- `getIntegritySummary()` / `startVerification()` / `getVerificationStatus(id)`
  / `getVerificationHistory(params)` — thin wrappers over the REST
  endpoints below, returning RxJS `Observable`s exactly like every other
  service in `core/services`.
- A small SSE client (`connectToVerification(id)`) using `fetch()` +
  manual `ReadableStream` frame parsing (see "SSE architecture" above for
  why not `EventSource`), exposing the live status as Angular signals
  (`status`, `entriesChecked`, `totalEntries`, `progressPercent`,
  `failureType`, `failureReason`) a component can bind to directly.

The dashboard itself replaces the **entirely fabricated** chain-
verification demo (`DmsStateService.verifyChain`'s `setInterval`-driven
fake sweep over 24 hardcoded nodes, and its `simulateTamper` toggle) that
previously occupied the "Blockchain Graph" tab of the existing `/app/audit`
screen — this was pre-existing scaffolding built before System 10's real
backend existed, explicitly documented in that service's own comment as
illustrative-only. Master prompt's "no client-side verification, no
simulated progress" makes replacing it in-scope, not optional. The
"Ledger Table" tab (individual audit **entries**, System 10's `GET
/audit`) is unchanged by this system — wiring that tab to real data is a
separate concern from chain **verification**, which is this system's
actual mandate.

Role-based UI hiding (the "Verify Audit Chain" button, the whole
dashboard section) is **UX only** — `auth.role() === 'ADMIN'` gates
rendering, exactly like `adminGuard` already gates the `/app/admin` route
— the backend's RBAC/RLS stack above is what actually enforces this; a
manipulated frontend gains nothing, since every API call independently
re-checks `audit:verify`.

## API Endpoints (System 11)

See [API_ENDPOINTS.md](./API_ENDPOINTS.md)'s "Audit" section for full
request/response detail. Summary:

| Method | Path | Purpose |
|---|---|---|
| `POST` | `/audit/verify-chain` | Start (or join an already-active) verification — `202` |
| `GET` | `/audit/verify-chain/:verificationId` | Current status/progress/result |
| `GET` | `/audit/verify-chain/:verificationId/events` | SSE progress stream |
| `GET` | `/audit/verifications` | Paginated verification history |
| `GET` | `/audit/integrity` | Dashboard summary (total entries, chain head, last run) |

## Limitations & Follow-Up

- `CANONICALIZATION_ERROR` is defensive: PostgreSQL's `jsonb` type
  guarantees syntactically valid JSON, so this path has no known
  realistic trigger via direct database tampering — documented, not
  tested via an integration tamper scenario (unlike every other failure
  type, which IS tamper-tested).
- A crashed worker's in-progress run is never resumed from its last
  checkpoint — it is marked `FAILED` (see "Stale verification recovery")
  and a fresh run starts from genesis. This is a deliberate simplicity
  choice (resuming correctly across a process restart is meaningfully
  more complex and was judged not to justify the cost for this system),
  not an oversight.
- The embedded-worker deployment (see "Job architecture") means
  verification throughput is bounded by the same process/connection pool
  as the HTTP server; a deployment that needs to scale verification
  independently would introduce a separate `cmd/worker` binary sharing
  `internal/jobs`/`internal/service` unchanged.

## Security Assumptions

- PostgreSQL is the sole source of truth for the chain. Nothing in this
  design stores authoritative chain state in Redis, MinIO, or application
  memory — Redis is not used for audit coordination at all in this
  system.
- The advisory lock key (`auditChainLockKey` in `internal/audit/chain.go`)
  is a fixed, arbitrary constant, reserved solely for this purpose.
- `evidentia_app` never holds `BYPASSRLS`, is never a superuser, and never
  owns `audit_log` — verified by dedicated tests, not merely asserted in
  this document.
- A privileged connection (a Postgres superuser, e.g. the migrator role)
  *can* still directly modify `audit_log`, exactly like any other table —
  RLS and `FORCE ROW LEVEL SECURITY` do not apply to a superuser. The
  cryptographic hash chain exists precisely so such tampering — whether
  by a compromised privileged credential or an operator mistake — is
  **detectable** after the fact via `POST /audit/verify-chain`, even
  though it cannot be *prevented* by permissions alone at that privilege
  level. This is the correct, intentional division of labor between the
  two defenses (permissions prevent; the hash chain detects), not a gap.
