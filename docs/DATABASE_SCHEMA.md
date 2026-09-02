# Database Schema

## Purpose

TODO: Document the full PostgreSQL schema for Evidentia.

## Entities

```text
users
roles
permissions
cases
case_users
documents
redactions
audit_log
compliance_certificates
refresh_tokens
```

## Conceptual Relationships

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

## Migrations

TODO: Document migration strategy (golang-migrate, up/down conventions,
naming scheme). See `backend/db/migrations/`.

## Row-Level Security

TODO: Document RLS policy design per table once implemented.

## JSONB Usage

TODO: Document which columns use JSONB and why (e.g. flexible metadata,
attribute bags for ABAC).

## sqlc Queries

TODO: Document query organization. See `backend/db/queries/`.
