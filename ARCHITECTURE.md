# Evidentia — High-Level Architecture

> This document describes the **intended** architecture. Systems 1-4 —
> Foundation & Infrastructure, Database & Data Layer, Authentication &
> Session Security, and Authorization (RBAC + ABAC + RLS integration) —
> are implemented (see below); everything under Request Flow and Core
> Domains past them is still a design reference. Note that System 4
> implements the *authorization infrastructure* only — the case/document/
> audit/admin HTTP routes it will eventually guard are a later system's
> scope and are not registered yet (see the System 4 section below).

## System 1 — Foundation & Infrastructure (Implemented)

```text
cmd/server/main.go
    |
    v
internal/app.New(ctx)
    |
    +--> internal/config.Load()     — typed, validated env config
    +--> internal/logger.New()      — structured slog logger
    +--> internal/database.New()    — pgx pool + Ping
    +--> internal/cache.New()       — go-redis client + Ping
    +--> internal/storage.NewMinIO()— MinIO client + bucket ensure
    |
    v
*app.App  (DI container: Config, Logger, DB, Cache, Storage)
    |
    v
internal/httpserver.NewRouter(app)
    |
    +--> middleware.Recovery       — panic -> safe JSON, logs stack server-side
    +--> middleware.RequestID      — validates/generates X-Request-ID
    +--> middleware.RequestLogger  — structured per-request log line
    +--> middleware.CORS           — configured origins/methods/headers
    +--> middleware.BodyLimit      — caps request body size
    |
    +--> GET /health   (handlers/health.Liveness)
    +--> GET /ready    (handlers/health.Readiness — pings DB/Cache/Storage)
    +--> NoRoute/NoMethod -> standard error envelope
```

`app.App` depends on `DBConn`/`CacheConn` interfaces (declared in
`internal/app`), not the concrete `*database.Database`/`*cache.Cache`
types — this is what lets tests substitute fakes for Postgres/Redis/MinIO
without Docker (see `internal/httpserver/router_test.go`).

Shutdown, triggered by SIGINT/SIGTERM: stop accepting new connections
(`http.Server.Shutdown`, bounded by `SERVER_SHUTDOWN_TIMEOUT`) → close
Redis → close the PostgreSQL pool → exit (`cmd/server/main.go`).

## System 2 — Database & Data Layer (Implemented)

```text
backend/db/migrations/000001_init_schema.{up,down}.sql
    |
    v
12 tables, RLS policies, current_app_user_id()/current_app_role(),
the evidentia_app runtime role (least-privilege, NOSUPERUSER, NOBYPASSRLS)
    |
    v
backend/db/queries/*.sql  --sqlc generate-->  backend/db/generated/
    |
    v
internal/models   (type aliases + controlled-vocabulary constants)
    |
    v
internal/repository
    |
    +--> UserRepo, CaseRepo, DocumentRepo, AuditRepo, CertificateRepo
    |      (thin wrappers over *generated.Queries)
    |
    +--> WithTx(ctx, pool, AppIdentity{UserID, Role}, fn)
           |
           v
         BEGIN -> set_config('app.user_id', ..., true)
                  set_config('app.role', ..., true)
               -> fn(ctx, q)
               -> COMMIT / ROLLBACK
```

`cmd/migrate` (a separate binary from `cmd/server`) applies migrations
using `DATABASE_MIGRATOR_USER`/`PASSWORD` — a privileged, schema-owning
role distinct from `evidentia_app`, which is all `cmd/server` ever
connects as. See [docs/DATABASE_SCHEMA.md](./docs/DATABASE_SCHEMA.md) for
the full schema, RLS design, and privilege model — including the audit-log
immutability guarantee verified in
`backend/tests/db_audit_privileges_test.go`.

`internal/database.Database` (System 1) still owns the connection pool
itself; System 2 adds the transaction/RLS-identity layer on top of it.
System 3's `AuthService` is the first code to actually query `users` and
(via its own migration) `auth_sessions` through this layer — see below.

## System 3 — Authentication & Session Security (Implemented)

```text
POST /api/v1/auth/{login,refresh,logout}
    |
    v
internal/handlers/auth   — bind/validate request, shape response
    |
    v
internal/service.AuthService
    |
    +--> internal/auth
    |      HashPassword/VerifyPassword (bcrypt)
    |      JWTManager.CreateAccessToken/Validate (HS256, golang-jwt/v5)
    |      GenerateRefreshToken/HashRefreshToken (crypto/rand + SHA-256)
    |      AuthenticatedUser + SetAuthenticatedUser/CurrentUser (gin context)
    |
    +--> internal/repository (System 2's WithTx + UserRepo/AuthSessionRepo)
    |      users, auth_sessions — via db/migrations/000002_auth_sessions
    |
    +--> internal/audit.Recorder (SlogRecorder — logs, does not persist
           to audit_log; System 8 provides the durable implementation)

internal/middleware.Auth (guards POST /auth/logout; later systems' routes
will use it too)
    |
    +--> JWTManager.Validate     — signature/algorithm/issuer/audience/exp
    +--> AuthService.ResolveIdentity — fresh status+role lookup, never
           trusting the JWT's role claim
    +--> auth.SetAuthenticatedUser  — attaches AuthenticatedUser to the
           gin.Context for downstream handlers (and, later, System 4)
```

`app.App` gained `JWTManager` and `AuthService` fields (constructed in
`app.New`, alongside the System 1 infrastructure clients) — `NewRouter`
wires them into the `/api/v1/auth` group and into `middleware.Auth`.
`middleware.Auth` depends on an `IdentityResolver` *interface* (satisfied
structurally by `*service.AuthService`), not the concrete service type —
the same testability pattern System 1 used for `app.DBConn`/`CacheConn`,
letting the middleware be unit-tested with a fake and no database. Full
design/security rationale: [docs/SECURITY.md](./docs/SECURITY.md).

## System 4 — Authorization: RBAC + ABAC + PostgreSQL RLS Integration (Implemented)

```text
internal/authz.Service
    |
    +--> HasPermission(ctx, user, action)          — RBAC
    |      loads user.Roles' permissions from roles/permissions/
    |      role_permissions (System 2), unioned across every role
    |
    +--> CanAccessCase(ctx, user, caseID, action)   — ABAC
    +--> CanAccessDocument(ctx, user, docID, action)— ABAC
    |      HasPermission first (cheap, no resource lookup), then loads the
    |      resource under the CALLER'S OWN transaction-local RLS identity
    |      (repository.WithTx) and independently re-derives owner/member
    |      status from the returned rows
    |
    +--> CanModifyUserRole(ctx, actor, targetUserID) — privilege-escalation guard
    +--> CanViewProtectedPartyDetails / SanitizeInvolvedParty — witness-identity policy
    |
    +--> internal/audit.Recorder — AUTHZ_DENIED events on every denial

internal/middleware.RequirePermission(authorizer, action)
    — RBAC gate: 401 (no authenticated user) / 403 (denied) / 500 (authorizer
      error) / next handler

internal/middleware.RequireCaseAccess / RequireDocumentAccess(authorizer, action, param)
    — ABAC gate: parses the path param as a UUID (malformed -> 403, same
      as a real denial), calls the matching Can* method, same status
      mapping as above
```

`app.App` gained an `AuthzService *authz.Service` field (constructed in
`app.New`, sharing the same `*pgxpool.Pool` and `audit.Recorder` as
`AuthService`). `internal/authz` depends only on `internal/auth`
(`AuthenticatedUser`) and `internal/repository` (`WithTx`) — no import
cycle, and no new external dependency (go.mod unchanged).

**What's genuinely new here** vs. what System 2 already built: System 2
already implemented full PostgreSQL RLS (`current_app_user_id()`/
`current_app_role()`, policies on every case/document-adjacent table) and
the transaction-local identity plumbing (`repository.WithTx`). System 4
adds the *application-layer* RBAC/ABAC decision engine that composes with
that RLS as defense-in-depth (neither layer trusts the other blindly —
see `docs/SECURITY.md`'s "PostgreSQL RLS integration"), the authorization
middleware, the witness-identity policy, and the privilege-escalation
guard for role modification.

**What's deliberately not here yet**: `internal/handlers/{case,document,
audit,user}` and `internal/service/{case,document,audit,user}_service.go`
remain TODO stubs (later systems' business logic), so no case/document/
audit/admin route exists in `internal/httpserver/router.go` for this
middleware to guard yet. `router.go` carries a comment showing the
intended wiring for whichever system adds those routes. Full design:
[docs/SECURITY.md](./docs/SECURITY.md); full test coverage (RBAC matrix,
ABAC case/document relationships, IDOR, privilege escalation, header/role
spoofing): `backend/internal/authz/*_test.go`,
`backend/internal/middleware/{rbac,abac}_middleware_test.go`,
`backend/tests/{rbac,abac}_test.go`.

## System 5 — Case Management & Case Lifecycle (Implemented)

```text
internal/handlers/case (package cases)
    |
    +--> Create/List/Get/Update — parse/validate request, read the
    |      already-authenticated user, delegate to CaseService, shape the
    |      response. No SQL, transaction, or audit write here.
    v
internal/service.CaseService
    |
    +--> independently re-checks authz.Service.HasPermission/CanAccessCase
    |      (service-layer authorization — see docs/SECURITY.md; not just
    |      trusting that HTTP middleware already ran)
    +--> validates input (case_number/title/description length, status
    |      enum, JSONB metadata shape/size — internal/utils.ValidateJSONMetadata)
    +--> enforces its OWN documented status-transition model
    |      (caseStatusTransitions) — System 2's schema only constrains the
    |      value set, not a transition graph
    +--> internal/repository.CaseRepo (ListFiltered/CountFiltered — new
    |      sqlc queries; Create/GetByID/Update/AddMember — existing)
    |      via repository.WithTx (transaction-local RLS identity)
    +--> internal/audit.Recorder — CASE_CREATED / CASE_UPDATED /
           CASE_STATUS_CHANGED events (never a false "success" event for a
           failed/rolled-back mutation)
```

`app.App` gained a `CaseService *service.CaseService` field. Routes
registered in `internal/httpserver/router.go` under `/api/v1/cases`:

```text
POST   /api/v1/cases      Auth + RequirePermission(case:create)
GET    /api/v1/cases      Auth + RequirePermission(case:read)
GET    /api/v1/cases/:id  Auth + RequireCaseAccess(case:read, "id")
PUT    /api/v1/cases/:id  Auth + RequireCaseAccess(case:update, "id")
```

— exactly the wiring System 4's `router.go` comment already sketched.

**Role-scoped listing** (`GET /cases`) is enforced entirely by PostgreSQL
RLS (System 2's `cases_select` policy), not by Go-side filtering: a
POLICE/LAWYER/FORENSICS/JUDGE caller's query only ever returns cases they
created or hold an active `case_members` row for; ADMIN sees all. The two
new sqlc queries (`ListCasesFiltered`/`CountCasesFiltered`,
`db/queries/cases.sql`) add optional status/case_number/title/created_by/
created_from/created_to filtering and pagination LIMIT/OFFSET entirely in
SQL, on top of whatever RLS already narrowed the result set to — never
"select everything, filter in Go". No docket table exists for JUDGE's
"authorized scope" — the safest supported interpretation (the same
`case_members` mechanism every other non-role uses) is implemented, with
finer-grained docket enforcement explicitly deferred (see
`docs/SECURITY.md`'s "Case-based ABAC").

**Case detail** (`GET /cases/:id`) assembles metadata, status,
witness-identity-sanitized involved parties (`authz.SanitizeInvolvedParty`
— reused as-is from System 4, now finally wired into a live handler),
document references (metadata only — never bytes, never MinIO), and a
chronological timeline synthesized from already-loaded case/document/
involved-party timestamps. It deliberately does NOT read `audit_log`: no
system populates that table yet (`audit.SlogRecorder` still writes only to
the operational log — System 8 owns the durable, hash-chained writer), so
building a timeline from it would either be empty or require this system
to invent chain-writing logic explicitly out of scope.

**IDOR posture** matches System 4's existing middleware exactly:
`CaseService.GetCase`/`UpdateCase` return the identical `403 FORBIDDEN`
generic-message error for a case that doesn't exist and a case the caller
has no relationship to (verified by `TestCaseFlow_EndToEnd`'s guessed-UUID
and malformed-UUID assertions).

**What's genuinely new here** vs. System 4: the first live case/document
handler package, the first `CaseService`, two new sqlc queries, no new
migration (System 2's `cases`/`case_members`/`case_involved_parties`/
`documents` schema and indexes were already sufficient), and no change to
any RLS policy or `evidentia_app` grant.

**What's deliberately not here**: document upload/download/verify/redact/
share, audit-chain computation/verification, and admin user management
remain later systems' scope — `internal/handlers/{document,audit,user}`
and their services remain TODO stubs. Full design:
[docs/SECURITY.md](./docs/SECURITY.md); tests:
`backend/internal/service/case_service_integration_test.go`,
`backend/internal/httpserver/case_flow_integration_test.go`,
`backend/tests/case_rls_test.go`.

## Request Flow (Intended, Later Systems)

```text
Frontend
    |
    | HTTPS / REST / SSE
    v
Gin API
    |
    +--> Authentication Middleware
    |
    +--> Authorization Middleware
    |       |
    |       +--> RBAC
    |       +--> ABAC
    |
    +--> Validation Middleware
    |
    +--> Audit Middleware
    |
    v
Handlers
    |
    v
Services
    |
    +-------------------+
    |                   |
    v                   v
Repository          Storage
(sqlc)              (MinIO)
    |
    v
PostgreSQL
    |
    +--> RLS
    +--> JSONB
    +--> Cases
    +--> Documents
    +--> Users
    +--> Roles
    +--> Audit Log
    +--> Certificates

Redis
    |
    v
Asynq Workers
    |
    +--> Audit Verification
    +--> Certificate Jobs
    +--> Background Processing
```

## Layering Principles

The codebase enforces strict separation of concerns:

```text
Handlers
   ↓
Services
   ↓
Repositories
   ↓
Database

Services
   ↓
Storage

Services
   ↓
Jobs

Jobs
   ↓
Redis / Asynq

Services
   ↓
Realtime / SSE
```

- **Handlers** parse/validate HTTP input and delegate to services. They
  contain no business logic.
- **Services** contain business logic and orchestrate repositories, storage,
  jobs, and realtime notifications.
- **Repositories** are the only layer that talks to the database, via
  sqlc-generated code.
- **Storage** abstracts object storage (MinIO today, local disk as an
  alternate implementation) behind a common interface.
- **Jobs** encapsulate asynchronous work dispatched to Redis/Asynq workers.
- **Realtime** manages SSE connections and event broadcasting.

## Core Domains

- **Auth** (implemented — System 3) — JWT issuance/validation, refresh
  tokens, password hashing.
- **RBAC/ABAC** (implemented — System 4) — Role- and attribute-based
  authorization (`internal/authz`), enforced above the database and
  reinforced by PostgreSQL RLS. The HTTP routes it will guard (cases,
  documents, audit, admin) are a later system's scope.
- **Cases** (implemented — System 5) — Case CRUD, role-scoped listing,
  status lifecycle, involved parties, case-user membership
  (`internal/service.CaseService`, `internal/handlers/case`).
- **Documents** — Evidence documents, integrity hashing, redaction lineage,
  compliance certificates.
- **Audit Chain** — Immutable, hash-chained audit log of security-sensitive
  actions.
- **Crypto** — SHA-256 integrity hashing, AES-256 encryption, RSA/ECDSA
  signing (future).
- **Storage** — MinIO-backed object storage behind a provider-agnostic
  interface.
- **Jobs** — Redis/Asynq-backed background processing.
- **Realtime** — SSE-based progress and notification streaming.

## Data Model Overview

Implemented — see [docs/DATABASE_SCHEMA.md](./docs/DATABASE_SCHEMA.md) for
the full ER diagram, every table's purpose, and the design decisions
behind each (UUID/timestamp/hash representation, why there's no
`agencies` table, why controlled-vocabulary columns use `CHECK` instead of
native `ENUM`, and more).

## Security Principles (Architectural Intentions)

The eventual system will enforce:

1. JWT authentication
2. RBAC
3. ABAC
4. PostgreSQL Row-Level Security
5. SHA-256 document integrity
6. AES-256 encryption
7. Immutable, append-only audit logs
8. Hash-chained audit entries
9. Transactional / concurrency-safe audit writing
10. TLS in transit
11. Secure refresh-token handling
12. Audit logging of all security-sensitive actions

As of System 4: **1** (JWT) and **11** (refresh-token rotation/revocation)
were implemented in System 3; **4** (RLS) was implemented in System 2,
enforced with policies and fail-closed behavior verified by integration
tests; **2** (RBAC) and **3** (ABAC) are implemented in System 4
(`internal/authz`), composed with RLS as defense-in-depth rather than
replacing it. Audit entries have their hash/prev_hash storage and
uniqueness invariants (**7**, **8**) in place, and failed/successful auth
actions plus authorization denials are already recorded operationally
(**12**, partial — see SECURITY.md), but *computing* the actual hash chain
and verifying it (**9**) is System 8's job. SHA-256 document hashing
(**5**), AES-256 (**6**), and TLS (**10**) remain unimplemented. See
[docs/SECURITY.md](./docs/SECURITY.md) and
[docs/DATABASE_SCHEMA.md](./docs/DATABASE_SCHEMA.md) for what each
currently covers.
