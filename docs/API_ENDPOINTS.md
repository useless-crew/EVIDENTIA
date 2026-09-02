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

TODO (business logic — not implemented). Required authorization, per
System 4 (`docs/SECURITY.md`'s Authorization section) — to be wired with
`middleware.RequirePermission`/`RequireCaseAccess` once these routes and
their handlers exist:

| Route | Permission (RBAC) | Resource check (ABAC) |
|---|---|---|
| `POST /cases` | `case:create` | — (no resource yet; the creator becomes `cases.created_by`) |
| `GET /cases` | `case:read` | list is scoped by the caller's own case relationships, not a per-item check here |
| `GET /cases/:id` | `case:read` | `CanAccessCase` — caller must be ADMIN, the case's creator, or an active `case_members` row |
| `PUT /cases/:id` | `case:update` | `CanAccessCase` (same relationship check) |

## Case Documents

```text
POST /cases/:id/documents
```

TODO (business logic — not implemented). Required authorization:
`document:upload` (RBAC) + `CanAccessCase` on the `:id` case (ABAC) — a
caller must have a relationship to the case before uploading into it.

## Documents

```text
GET  /documents/:id
GET  /documents/:id/download
POST /documents/:id/verify
POST /documents/:id/redact
POST /documents/:id/share
GET  /documents/:id/certificate
```

TODO (business logic — not implemented). Required authorization:

| Route | Permission (RBAC) | Resource check (ABAC) |
|---|---|---|
| `GET /documents/:id` | `document:read` | `CanAccessDocument` |
| `GET /documents/:id/download` | `document:download` | `CanAccessDocument` |
| `POST /documents/:id/verify` | `document:verify` | `CanAccessDocument` |
| `POST /documents/:id/redact` | `document:redact` | `CanAccessDocument` |
| `POST /documents/:id/share` | `document:share` | `CanAccessDocument` |
| `GET /documents/:id/certificate` | `certificate:read` | `CanAccessDocument` on the certificate's document |

`CanAccessDocument` resolves the document's case and applies the same
case-relationship check as `CanAccessCase` — see `docs/SECURITY.md`'s
"Document-based ABAC".

## Audit

```text
GET  /audit
POST /audit/verify-chain
```

TODO (business logic — not implemented). Required authorization:
`GET /audit` needs `audit:read`; `POST /audit/verify-chain` needs
`audit:verify`. Per the seed data, only ADMIN and POLICE hold `audit:read`
today (POLICE at case scope, once a future system adds `GET /audit`'s own
case filtering — this endpoint has no resource ID of its own to run ABAC
against), and only ADMIN holds `audit:verify`.

## Admin

```text
POST /admin/users
PUT  /admin/users/:id
PUT  /admin/users/:id/role
GET  /admin/roles
```

TODO (business logic — not implemented). Required authorization:
`POST /admin/users` needs `user:create`; `PUT /admin/users/:id` needs
`user:update`; `PUT /admin/users/:id/role` needs
`internal/authz.Service.CanModifyUserRole` (RBAC `user:role` PLUS an
explicit block on an actor modifying their own role — see
`docs/SECURITY.md`'s "Privilege escalation / admin boundaries"); `GET
/admin/roles` needs no special permission beyond authentication (it lists
the fixed, non-sensitive role catalog). Per the seed data, only ADMIN
holds `user:create`/`user:update`/`user:role` today.

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

`internal/middleware.Auth` validates a request's JWT and re-resolves the
caller's current account status/roles from the database (never trusting
the JWT's role claim alone — see SECURITY.md) — this establishes
*identity*, not *authorization*.

System 4 (`internal/authz`) provides the RBAC (`middleware.RequirePermission`)
and ABAC (`middleware.RequireCaseAccess`/`RequireDocumentAccess`) checks
layered on top of it, and the per-route requirements are documented inline
above (Cases/Case Documents/Documents/Audit/Admin) — but no case/document/
audit/admin route is registered in `internal/httpserver/router.go` yet
(their handlers are still TODO stubs), so none of that middleware is wired
into a live route today. Today's only non-health routes are
`/api/v1/auth/{login,refresh,logout}`, which need no RBAC/ABAC (see
"Authentication" above). Full authorization design: `docs/SECURITY.md`'s
Authorization section.

`401` vs `403`: a request with no/invalid/expired authentication is
always `401 UNAUTHORIZED`; an authenticated request denied by RBAC or ABAC
is `403 FORBIDDEN` with the generic message `"You do not have permission
to perform this action"` — never a message naming the specific
permission, case, or document relationship that failed.
