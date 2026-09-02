# Evidentia — High-Level Architecture

> This document describes the **intended** architecture. It is a design
> reference for the scaffolding created in this repository. No part of it is
> implemented yet.

## Request Flow

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

None of the above is implemented in this scaffolding phase. See
[docs/SECURITY.md](./docs/SECURITY.md).
