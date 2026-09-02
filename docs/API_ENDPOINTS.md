# API Endpoints

## Purpose

TODO: Document the full REST API surface for Evidentia. The domain
endpoints below (Authentication, Cases, Documents, Audit, Admin) are not
implemented yet — this section documents the intended surface. Health and
Readiness, at the bottom, are implemented today.

## Authentication

```text
POST /auth/login
POST /auth/refresh
POST /auth/logout
```

TODO: Document request/response schemas, status codes, and error cases.

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

TODO: Document per-endpoint auth requirements (roles, permissions, attributes).
