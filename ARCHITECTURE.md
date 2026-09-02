# Evidentia — High-Level Architecture

> This document describes the **intended** architecture. Systems 1-3 —
> Foundation & Infrastructure, Database & Data Layer, and Authentication &
> Session Security — are implemented (see below); everything under Request
> Flow and Core Domains past them is still a design reference.

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
- **RBAC/ABAC** (not implemented — System 4) — Role- and attribute-based
  authorization, enforced above the database and reinforced by PostgreSQL
  RLS.
- **Cases** — Case lifecycle and case-user membership.
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

As of System 3: **1** (JWT) and **11** (refresh-token rotation/revocation)
are implemented; **4** (RLS) was implemented in System 2, enforced with
policies and fail-closed behavior verified by integration tests. Audit
entries have their hash/prev_hash storage and uniqueness invariants
(**7**, **8**) in place, and failed/successful auth actions are already
recorded operationally (**12**, partial — see SECURITY.md), but
*computing* the actual hash chain and verifying it (**9**) is System 8's
job. SHA-256 document hashing (**5**), AES-256 (**6**), TLS (**10**),
RBAC (**2**), and ABAC (**3**) remain entirely unimplemented. See
[docs/SECURITY.md](./docs/SECURITY.md) and
[docs/DATABASE_SCHEMA.md](./docs/DATABASE_SCHEMA.md) for what each
currently covers.
