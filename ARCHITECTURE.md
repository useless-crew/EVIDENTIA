# Evidentia — High-Level Architecture

> This document describes the **intended** architecture. System 1 —
> Foundation & Infrastructure — is implemented (see below); everything
> under Request Flow and Core Domains past it is still a design reference.

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

- **Auth** — JWT issuance/validation, refresh tokens, password hashing.
- **RBAC/ABAC** — Role- and attribute-based authorization, enforced above the
  database and reinforced by PostgreSQL RLS.
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

See [docs/DATABASE_SCHEMA.md](./docs/DATABASE_SCHEMA.md) for full detail.

```text
User
 └── Role

Case
 ├── Users
 ├── Documents
 └── Audit Events

Document
 ├── Case
 ├── Parent Document
 ├── SHA-256 Hash
 ├── MinIO Object Reference
 └── Certificate

Redaction
 ├── Source Document
 └── Derivative Document

AuditLog
 ├── User
 ├── Resource
 ├── Entry Hash
 └── Previous Hash
```

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

None of the above is implemented yet — System 1 covers infrastructure only
(see [docs/SECURITY.md](./docs/SECURITY.md) for what that includes).
