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

## Relationship to Async Work

Explicitly **out of scope** for this system, by design (see the top-level
task scope): a background job dispatching chain verification via
Redis/Asynq, an SSE-streamed live progress UI, and a frontend audit
dashboard. `AuditService.VerifyChain`'s batched, resumable (`next_seq`)
design was chosen specifically so a future system can build that
experience on top of it (repeated calls, each covering one bounded slice
of work) without requiring any change to the verification logic itself —
but no such job, queue consumer, or streaming endpoint exists yet.
`internal/jobs/audit_verification.go` remains the placeholder it was
before this system, intentionally.

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
