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

Redis/Asynq are intended to eventually support:

- Long-running audit-chain verification
- Certificate generation
- Background document processing
- Future OCR/AI workloads
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

## Explicitly Out of Scope

The following substitutions are **not** permitted without a formal
architecture decision:

- Replacing PostgreSQL with another database engine
- Replacing MinIO with another object store
- Replacing Gin with another HTTP framework
- Replacing Go with another language
- Adding frameworks/libraries outside this list without justification
