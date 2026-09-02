# API Endpoints

## Purpose

TODO: Document the full REST API surface for Evidentia.

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

## Response Envelope

See [../backend/pkg/response/response.go](../backend/pkg/response/response.go)
for the standard response/error envelope shape.

## Authentication & Authorization Requirements

TODO: Document per-endpoint auth requirements (roles, permissions, attributes).
