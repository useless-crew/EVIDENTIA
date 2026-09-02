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

## Principles

The eventual system will enforce all twelve of these. Implemented so far:
**1** (System 3), **4** (System 2), **11** (System 3). Partial: **12**
(failed/successful auth actions are recorded today via
`internal/audit.Recorder`, but only to the operational log — see
"Audit integration" above — not the durable table); **7**/**8** (audit_log's
storage invariants exist — System 2 — but nothing computes a hash chain
yet). Not started: 2, 3, 5, 6, 9, 10.

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

Not implemented — this is System 4's scope. What System 3 provides for it
to build on: every authenticated request carries a fresh
`auth.AuthenticatedUser{ID, Email, Roles}` (re-resolved from the database,
not a cached JWT claim — see Authentication above), accessible via
`auth.CurrentUser(c)`. TODO once System 4 lands: document the RBAC role/
permission model, ABAC attribute policies, and how they compose with
PostgreSQL RLS as defense-in-depth.

## Cryptography

TODO: Document SHA-256 integrity hashing, AES-256 encryption key management,
and the future RSA/ECDSA signing module.

## Transport Security

TODO: Document TLS configuration and certificate management.

## Threat Model

TODO: Document assumptions, trust boundaries, and mitigations relevant to
investigative/judicial evidence handling.
