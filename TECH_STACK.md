# Evidentia — Technology Stack

This document is the **authoritative** technology stack for the Evidentia
backend. It reflects decisions already made; it is not a menu of options.

## Core

- Go 1.22+
- Gin (HTTP framework)
- REST API
- Server-Sent Events (SSE) for real-time server-to-client progress updates

## Database

- PostgreSQL 15+
- sqlc (type-safe generated query code)
- PostgreSQL Row-Level Security (RLS)
- JSONB for flexible/semi-structured columns
- golang-migrate for schema migrations

## Object Storage

- MinIO
- S3-compatible API

## Authentication

- JWT (`golang-jwt/jwt/v5`)
- bcrypt for password hashing

## Authorization

- RBAC (Role-Based Access Control)
- ABAC (Attribute-Based Access Control)
- PostgreSQL RLS (defense-in-depth at the database layer)

## Security / Cryptography

- SHA-256 for document integrity hashing
- AES-256 for encryption at rest
- RSA/ECDSA reserved for a future digital-signature module

## Async Processing

- Redis
- Asynq

Implemented (System 11, generalized as reusable infrastructure by Systems
12 and 13): long-running audit-chain verification (`internal/jobs`,
`internal/events`, `internal/sse` — see docs/AUDIT_CHAIN.md,
docs/BACKGROUND_JOBS.md, and docs/REALTIME_EVENTS.md). The worker runs
embedded in the same process as the HTTP server (`cmd/server/main.go`),
not a separate deployment unit; Redis's role is Asynq's queue transport
(plus queue priority — see `internal/jobs.QueueCritical`/`QueueDefault`)
AND (System 13) a single Pub/Sub channel real-time event notifications
travel over (`internal/events.Channel`) — two independent uses of the
one Redis instance, never conflated (Asynq is for job execution; Pub/Sub
is for event delivery) — PostgreSQL remains the authoritative store for
both verification state and every fact an event describes. System 12
evaluated certificate generation and redaction as candidates and
deliberately kept both synchronous — see docs/BACKGROUND_JOBS.md's "Task
Types".

Not yet used for:

- Certificate generation (evaluated, kept synchronous — see above)
- Redaction (evaluated, kept synchronous — see above)
- Future OCR/AI workloads (no such pipeline exists yet)
- Other asynchronous jobs

## Validation

- `go-playground/validator`

## Configuration

- Environment variables
- `godotenv` and/or `viper`

## API Documentation

- Swagger / OpenAPI via `swaggo/swag`

## Testing

- Go `testing` package
- Testify

## Deployment

- Docker
- Docker Compose

## Implementation Status

**System 1 (Foundation & Infrastructure):** Go, Gin, PostgreSQL
(`pgx`/`pgxpool`), Redis (`go-redis`), MinIO (`minio-go`), `godotenv`,
Testify.

**System 2 (Database & Data Layer):** PostgreSQL Row-Level Security,
`sqlc` (CLI, not a Go dependency — see `backend/sqlc.yaml`),
`golang-migrate` (as a Go library, `cmd/migrate`), `google/uuid` (sqlc's
Go-side UUID representation).

**System 3 (Authentication & Session Security):** `golang-jwt/jwt/v5`,
`golang.org/x/crypto/bcrypt`, `swaggo/swag` (both the CLI generator and
its small runtime registration package, `github.com/swaggo/swag`, which
generated `docs.go` files import).

**System 4 (Authorization):** no new dependency — `internal/authz` and its
middleware (`internal/middleware/{rbac,abac}_middleware.go`) are plain Go
using only what's already listed above (no Casbin, no OPA, no external
policy server; see master-prompt-driven design rationale in
docs/SECURITY.md).

**System 5 (Case Management)** / **System 6 (Document Management)**: no
new dependency — `pkg/hash` (streaming SHA-256, Go standard library
`crypto/sha256`) is System 6's own addition, already covered by "Security
/ Cryptography" above.

**System 7 (Evidence Verification & Compliance Certificates):** no new
dependency — `pkg/crypto` (ECDSA P-256 signing, Go standard library
`crypto/ecdsa`/`crypto/x509`) fills in the ECDSA half of "RSA/ECDSA
reserved for a future digital-signature module" above; RSA remains
unimplemented (`pkg/crypto/rsa_sign.go` is still a TODO stub — no system
through 7 needs it).

**System 11 (Audit Chain Verification & Integrity Dashboard):** adds
`github.com/hibiken/asynq` (Redis-backed task queue) and SSE
(`net/http`/Gin's own streaming response support — no new dependency for
SSE itself) — both already reserved for exactly this use in "Core"/"Async
Processing" above. See docs/AUDIT_CHAIN.md.

**System 12 (Asynchronous Processing & Background Jobs):** no new
dependency — generalizes System 11's existing `github.com/hibiken/asynq`
usage into reusable infrastructure (`internal/jobs`); no new library was
needed. See docs/BACKGROUND_JOBS.md.

**System 13 (Real-Time Events & Server-Sent Events):** no new
dependency — generalizes System 11's existing SSE handling and the
already-connected `github.com/redis/go-redis/v9` client (via
`internal/cache.Cache`) into reusable infrastructure (`internal/events`,
`internal/sse`); no new library was needed. See docs/REALTIME_EVENTS.md.

Not yet added, pending the systems that need them: AES-256, RSA,
`go-playground/validator`. Adding any of these before their owning system
is implemented is scope creep — don't.

## Explicitly Out of Scope

The following substitutions are **not** permitted without a formal
architecture decision:

- Replacing PostgreSQL with another database engine
- Replacing MinIO with another object store
- Replacing Gin with another HTTP framework
- Replacing Go with another language
- Adding frameworks/libraries outside this list without justification
