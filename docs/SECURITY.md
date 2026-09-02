# Security

## Purpose

TODO: Document the full security model for Evidentia.

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
