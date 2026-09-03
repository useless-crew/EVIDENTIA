# API Endpoints

## Purpose

TODO: Document the full REST API surface for Evidentia. Cases (below),
Authentication, Health, and Readiness are implemented today. Documents,
Audit, and Admin are not implemented yet — those sections document the
intended surface only.

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

## Cases (implemented — System 5)

```text
POST   /api/v1/cases
GET    /api/v1/cases
GET    /api/v1/cases/:id
PUT    /api/v1/cases/:id
```

All four require `Authorization: Bearer <access_token>`. Required
authorization, per System 4 (`docs/SECURITY.md`'s Authorization section),
wired via `middleware.RequirePermission`/`RequireCaseAccess`:

| Route | Permission (RBAC) | Resource check (ABAC) |
|---|---|---|
| `POST /cases` | `case:create` | — (no resource yet; the creator becomes `cases.created_by`, never a client-supplied value) |
| `GET /cases` | `case:read` | list is scoped by PostgreSQL RLS (the caller's own case relationships), not a per-item check here |
| `GET /cases/:id` | `case:read` | `CanAccessCase` — caller must be ADMIN, the case's creator, or an active `case_members` row |
| `PUT /cases/:id` | `case:update` | `CanAccessCase` (same relationship check) |

`internal/service.CaseService` independently re-checks the same
authorization internally (not just the HTTP middleware) — see
`ARCHITECTURE.md`'s System 5 section.

### `POST /cases`

```json
{"case_number": "FIR-2026-001", "title": "Theft investigation", "description": "...", "status": "OPEN", "metadata": {}}
```

`status`/`description`/`metadata` are all optional (`status` defaults to
`OPEN`). `id`/`created_by`/`created_at`/`updated_at` are server-controlled
— there is no request field for any of them, so a client cannot supply
one even accidentally. On success (`201`), the response is the same
shape `GET /cases/:id` returns (see below). The creator is also added as
an `OWNER` `case_members` row in the same transaction.

- `400` — missing/invalid `case_number`/`title`, invalid `status`, or
  malformed/oversized `metadata` (see `internal/utils.ValidateJSONMetadata`
  — max 32KB, must be a JSON object).
- `409` — `case_number` already exists (`cases_case_number_unique`).

### `GET /cases`

Query parameters (all optional): `status`, `case_number` (substring
match), `title` (substring match), `created_by` (UUID), `created_from`/
`created_to` (RFC3339 timestamps), `page` (default 1), `page_size`
(default 20, max 100 — larger values are silently clamped, not rejected).
Filtering and pagination both happen in SQL
(`db/queries/cases.sql`'s `ListCasesFiltered`/`CountCasesFiltered`), on
top of whatever PostgreSQL RLS already narrowed the row set to — never an
unfiltered `SELECT *` followed by in-memory filtering.

```json
{
  "success": true,
  "data": {
    "cases": [{"id": "...", "case_number": "...", "title": "...", "status": "OPEN", "created_by": "...", "created_at": "...", "updated_at": "..."}],
    "meta": {"page": 1, "page_size": 20, "total": 1, "total_pages": 1}
  }
}
```

### `GET /cases/:id`

```json
{
  "success": true,
  "data": {
    "id": "...", "case_number": "...", "title": "...", "description": "...",
    "status": "OPEN", "metadata": {}, "created_by": "...", "created_at": "...", "updated_at": "...",
    "involved_parties": [{"id": "...", "party_type": "WITNESS", "display_name": "[REDACTED]", "metadata": {}, "created_at": "..."}],
    "documents": [{"id": "...", "document_type": "OTHER", "filename": "...", "mime_type": "...", "file_size": 0, "status": "ACTIVE", "uploaded_by": "...", "uploaded_at": "..."}],
    "timeline": [{"type": "CASE_CREATED", "timestamp": "...", "summary": "...", "related_id": "..."}],
    "relationship": {"is_owner": true, "is_member": true, "membership_type": "OWNER"}
  }
}
```

`involved_parties[].display_name`/`metadata` are redacted to `"[REDACTED]"`/
`{}` for a `WITNESS`-type party when the caller is not JUDGE/POLICE/ADMIN
— see `internal/authz.SanitizeInvolvedParty`. `documents` is metadata/
references only (never file bytes; capped at 50 rows — full paginated
document listing is System 6's scope). `timeline` is synthesized from
case/document/involved-party timestamps, not read from `audit_log` (see
`ARCHITECTURE.md`'s System 5 section for why).

- `403` — no relationship to this case, OR the case doesn't exist, OR the
  `:id` isn't a valid UUID. All three produce the byte-identical response
  (same status, same generic message) — never a `404` that would confirm
  a specific case ID's existence to an unauthorized caller.

### `PUT /cases/:id`

Full replacement — `title`, `status`, and `metadata` are always required
in the body (`description` optional); this mirrors the existing
`UpdateCase` SQL query, which was already an unconditional 4-column
`UPDATE`, not a partial-patch contract.

```json
{"title": "Updated title", "description": "...", "status": "UNDER_INVESTIGATION", "metadata": {}}
```

Response: same shape as `GET /cases/:id`. `id`/`created_by`/`created_at`
cannot be changed — the request body has no field for any of them.

- `400` — invalid `status` value, OR a status transition not in
  `CaseService`'s documented transition map (`OPEN` → `UNDER_INVESTIGATION`
  → `SUBMITTED` → `UNDER_REVIEW` → `CLOSED` → `ARCHIVED`, with
  `SUBMITTED`/`UNDER_REVIEW` allowed to fall back one step and `ARCHIVED`
  reachable directly from any non-terminal status). This is System 5's own
  conservative starting model — System 2's schema only constrains the
  value set, not a transition graph.
- `403` — same anti-enumeration behavior as `GET /cases/:id`.

### Audit integration

Every successful `POST`/`PUT` records an event via `internal/audit.Recorder`
(the same interface System 3/4 already use — operational log today, System
8's durable hash-chained writer later, no change required here):
`CASE_CREATED` on create; `CASE_UPDATED` on every successful update, plus a
separate `CASE_STATUS_CHANGED` event when `status` actually changed. A
failed/rejected mutation (validation error, authorization denial, invalid
transition, duplicate `case_number`) never produces one of these — see
`case_service_integration_test.go`'s `TestCaseService_*_Denied`/
`*Rejected`/`*Conflict` tests, which assert exactly that.

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
above (Cases/Case Documents/Documents/Audit/Admin). As of System 5, the
Cases routes are live and wired with exactly that middleware (see
`internal/httpserver/router.go`); Case Documents/Documents/Audit/Admin
routes remain unregistered (their handlers are still TODO stubs). Today's
non-health routes are `/api/v1/auth/{login,refresh,logout}` (no RBAC/ABAC
— see "Authentication" above) and `/api/v1/cases`/`/api/v1/cases/:id`.
Full authorization design: `docs/SECURITY.md`'s Authorization section.

`401` vs `403`: a request with no/invalid/expired authentication is
always `401 UNAUTHORIZED`; an authenticated request denied by RBAC or ABAC
is `403 FORBIDDEN` with the generic message `"You do not have permission
to perform this action"` — never a message naming the specific
permission, case, or document relationship that failed.
