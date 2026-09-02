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
  interface — see "Audit integration" below for why this logs rather than
  writes to the hash-chained `audit_log` table (that's System 8's job).

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

## Principles

The eventual system will enforce all twelve of these. Implemented so far:
**1** (System 3), **2**/**3** (System 4), **4** (System 2), **11**
(System 3). Partial: **12** (failed/successful auth actions, and now
authorization denials, are recorded via `internal/audit.Recorder`, but
only to the operational log — see "Audit integration" above — not the
durable table); **7**/**8** (audit_log's storage invariants exist —
System 2 — but nothing computes a hash chain yet). Not started: 5, 6, 9,
10.

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
today, `audit.SlogRecorder`, writes to the structured operational log, not
to the hash-chained `audit_log` table: actually computing `hash`/
`prev_hash` correctly is System 8's job (see DATABASE_SCHEMA.md's
"Audit-chain storage invariants"), and writing *unchained* rows into that
table now would risk breaking System 8's eventual chain verification over
historical data. `AuthService` depends only on the `Recorder` interface,
so swapping in System 8's real writer later requires no change to
authentication code. `Recorder.Record` never returns an error: a login or
refresh must not fail merely because audit logging had a hiccup (see
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

**Deferred**: no handler in the repository today reads/returns
`case_involved_parties` (that table's HTTP surface belongs to a later
system) — this policy exists as a ready-made, unit-tested enforcement
point (`internal/authz/protected_info_test.go`) for that future handler to
call, rather than being wired into a live endpoint today. The schema also
has no classification finer than `party_type`, so this can only redact at
the "is this a WITNESS record" granularity — a finer-grained
classification would need a schema change owned by whichever system needs
it.

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
become an authorization bypass). `audit.SlogRecorder` remains the only
implementation today — System 8 provides the durable, hash-chained writer
with no change required here.

### What System 4 does *not* do

- **Business logic for cases/documents/audit/admin** — `internal/handlers/
  {case,document,audit,user}` and `internal/service/{case,document,audit,
  user}_service.go` remain the TODO stubs later systems will implement.
  Because those routes are not yet registered in `internal/httpserver/
  router.go`, there is nothing to attach `RequirePermission`/
  `RequireCaseAccess`/`RequireDocumentAccess` to yet — this system
  provides the primitives (and `app.App.AuthzService`) ready for the
  system that adds those routes, per `internal/httpserver/router.go`'s
  comment showing the intended wiring.
- **Membership-type-specific action gating** — see "Case-based ABAC"
  above.
- **Finer-grained protected-information classification** beyond
  `party_type = 'WITNESS'` — see "Protected information" above.
- **The audit hash chain** — unchanged, still System 8's job.

## Cryptography

TODO: Document SHA-256 integrity hashing, AES-256 encryption key management,
and the future RSA/ECDSA signing module.

## Transport Security

TODO: Document TLS configuration and certificate management.

## Threat Model

TODO: Document assumptions, trust boundaries, and mitigations relevant to
investigative/judicial evidence handling.
