# API Endpoints

## Purpose

TODO: Document the full REST API surface for Evidentia. The domain
endpoints below (Cases, Documents, Audit, Admin) are not implemented yet —
this section documents the intended surface. Authentication (below),
Health, and Readiness (at the bottom) are implemented today.

## Authentication (implemented)

All under `/api/v1/auth`. Machine-readable spec:
`backend/docs/swagger/swagger.json` (generate via `make swagger`; not
committed — see `backend/docs/swagger/README.md`). Full design rationale:
[SECURITY.md](./SECURITY.md).

```http
POST /api/v1/auth/login
```

Public — no Authorization header required.

```json
{"email": "user@example.com", "password": "at-least-8-characters"}
```

```json
{
  "success": true,
  "data": {
    "access_token": "...",
    "refresh_token": "...",
    "token_type": "Bearer",
    "expires_in": 900,
    "user": {"id": "...", "email": "...", "first_name": "...", "last_name": "...", "role": "LAWYER"}
  }
}
```

- `400` — malformed body (missing/invalid email, password under 8 characters)
- `401` — `{"success":false,"error":{"code":"UNAUTHORIZED","message":"Invalid email or password","request_id":"..."}}`
  for **any** of: unknown email, wrong password, inactive/suspended account
  — deliberately identical in every case, to avoid revealing which one
  occurred (user enumeration).

```http
POST /api/v1/auth/refresh
```

Public. Rotates the presented refresh token: it is revoked and a new
access + refresh token pair (same session family) is issued.

```json
{"refresh_token": "..."}
```

Same success shape as login. `401` (same generic body as login, message
`"Invalid or expired refresh token"`) for: unknown/malformed token,
already-rotated (reused) token — which also revokes the entire token
family — expired token, or an inactive account.

```http
POST /api/v1/auth/logout
```

**Requires** `Authorization: Bearer <access_token>` (this is the one
exception to "auth routes are public" — see SECURITY.md for why). Revokes
the session identified by the supplied refresh token, if any.

```json
{"refresh_token": "..."}
```

`refresh_token` is optional — omitting it (or passing one already invalid)
still returns `200`; there is nothing else to invalidate (access tokens
are stateless). `401` if the Authorization header is missing/invalid/
expired, or the account has been deactivated since the token was issued.

```json
{"success": true, "data": null}
```

## Cases

```text
POST   /cases
GET    /cases
GET    /cases/:id
PUT    /cases/:id
```

TODO

## Case Documents

```text
POST /cases/:id/documents
```

TODO

## Documents

```text
GET  /documents/:id
GET  /documents/:id/download
POST /documents/:id/verify
POST /documents/:id/redact
POST /documents/:id/share
GET  /documents/:id/certificate
```

TODO

## Audit

```text
GET  /audit
POST /audit/verify-chain
```

TODO

## Admin

```text
POST /admin/users
PUT  /admin/users/:id
PUT  /admin/users/:id/role
GET  /admin/roles
```

TODO

## Health and Readiness (implemented)

These two endpoints are infrastructure probes, not domain API — they return
a flat JSON shape rather than the standard envelope below, and sit outside
`/api/v1` (see `internal/handlers/health`).

```http
GET /health
```

```json
{"status": "ok", "service": "evidentia-backend", "version": "dev"}
```

Always cheap; never touches PostgreSQL, Redis, or MinIO.

```http
GET /ready
```

```json
{"status": "ready", "dependencies": {"postgres": "ok", "redis": "ok", "minio": "ok"}}
```

Returns `200` when every dependency is reachable, `503` otherwise (with the
failing dependency marked `"error"` — never a raw driver error or
connection string).

## Response Envelope

Every other endpoint uses the standard envelope — see
[../backend/pkg/response/response.go](../backend/pkg/response/response.go):

```json
{"success": true, "data": {}}
{"success": false, "error": {"code": "NOT_FOUND", "message": "...", "request_id": "..."}}
```

Unmatched routes and disallowed methods already return this envelope today
(`NOT_FOUND` / `404`, `METHOD_NOT_ALLOWED` / `405`), implemented in
`internal/httpserver/router.go`.

## Authentication & Authorization Requirements

Today: `internal/middleware.Auth` validates a request's JWT and re-resolves
the caller's current account status/roles from the database (never
trusting the JWT's role claim alone — see SECURITY.md) — this establishes
*identity*, not *authorization*. No endpoint yet enforces role/permission-
based access control (RBAC) or attribute-based rules (ABAC); that is
System 4's scope. TODO once System 4 lands: document per-endpoint
required roles/permissions/attributes here.
