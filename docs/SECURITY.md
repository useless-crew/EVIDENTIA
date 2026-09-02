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

## Principles

The eventual system will enforce:

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

TODO: Document JWT access/refresh token lifecycle, bcrypt password hashing
policy, and session handling.

## Authorization

TODO: Document RBAC role/permission model, ABAC attribute policies, and how
they compose with PostgreSQL RLS as defense-in-depth.

## Cryptography

TODO: Document SHA-256 integrity hashing, AES-256 encryption key management,
and the future RSA/ECDSA signing module.

## Transport Security

TODO: Document TLS configuration and certificate management.

## Threat Model

TODO: Document assumptions, trust boundaries, and mitigations relevant to
investigative/judicial evidence handling.
