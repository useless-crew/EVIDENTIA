# Evidentia — AWS Security, Integrity & Compliance Architecture

This document establishes the security architecture, threat model, cryptographic controls, and compliance posture for Evidentia on AWS.

---

## 1. Threat Model & Security Posture

Evidentia manages sensitive digital evidence, court documents, and forensic audit trails. The system is designed to withstand:
- **Unauthorized Evidence Access:** Mitigated via ABAC checks and database-level PostgreSQL RLS.
- **Evidence Tampering & Mutation:** Mitigated via streaming SHA-256 hashing, immutable MinIO storage, non-destructive redactions, and Hyperledger Fabric blockchain anchoring.
- **Audit Ledger Alteration:** Mitigated by append-only database privileges, cryptographic SHA-256 hash chaining, and background Asynq verification.
- **Credential Stuffing & Brute Force:** Mitigated by dual-layer Redis rate limiting (per-IP and per-account).
- **Insider Threats / Database Administrator Access:** Mitigated by cryptographic hash verification, digital signatures, and independent blockchain anchors.

---

## 2. PostgreSQL Row-Level Security (RLS) Implementation

### 2.1 Defense-in-Depth Model
While the application layer enforces RBAC (roles) and ABAC (case membership and share delegation), PostgreSQL RLS provides a cryptographic barrier at the data layer. Even if an application-layer handler contains an IDOR vulnerability, PostgreSQL will automatically filter rows or abort unauthorized writes.

### 2.2 Forced RLS Enforcement
Every evidence, case, audit, and share table enforces RLS unconditionally:
```sql
ALTER TABLE cases ENABLE ROW LEVEL SECURITY;
ALTER TABLE cases FORCE ROW LEVEL SECURITY;

ALTER TABLE documents ENABLE ROW LEVEL SECURITY;
ALTER TABLE documents FORCE ROW LEVEL SECURITY;

ALTER TABLE audit_log ENABLE ROW LEVEL SECURITY;
ALTER TABLE audit_log FORCE ROW LEVEL SECURITY;
```
`FORCE ROW LEVEL SECURITY` ensures policies apply even to table owners.

### 2.3 Transaction-Local Session Injection
The Go backend interacts with PostgreSQL exclusively through `repository.WithTx`:
```go
func WithTx(ctx context.Context, pool *pgxpool.Pool, ident AppIdentity, fn func(ctx context.Context, q *generated.Queries) error) error {
    tx, err := pool.Begin(ctx)
    if err != nil { return err }
    defer tx.Rollback(ctx)

    // The third parameter 'true' restricts the setting strictly to the current transaction.
    // When the connection returns to the pool, the setting automatically vanishes.
    if _, err := tx.Exec(ctx, `SELECT set_config('app.user_id', $1, true)`, userID); err != nil {
        return err
    }
    if _, err := tx.Exec(ctx, `SELECT set_config('app.role', $1, true)`, ident.Role); err != nil {
        return err
    }
    return fn(ctx, generated.New(tx))
}
```

### 2.4 Least-Privilege Database Role
The application connects as `evidentia_app`, which is explicitly denied superuser and `BYPASSRLS` privileges:
```sql
CREATE ROLE evidentia_app WITH LOGIN PASSWORD '...';
-- No SUPERUSER, No CREATEDB, No CREATEROLE, No BYPASSRLS
GRANT CONNECT ON DATABASE evidentia TO evidentia_app;
GRANT USAGE ON SCHEMA public TO evidentia_app;
-- Table privileges restricted; audit_log is strictly SELECT and INSERT only (no UPDATE, no DELETE)
REVOKE UPDATE, DELETE, TRUNCATE ON audit_log FROM evidentia_app;
```

---

## 3. Cryptographic Evidence Integrity Lifecycle

```
[ Incoming File Stream ]
           |
           +---> [ SHA-256 Streaming Digest ] ---> Stored in DB (32-byte BYTEA)
           |
           +---> [ MinIO Object Store ] (Private EC2 gp3 EBS)
           |
           +---> [ Chained Audit Log Entry ] ---> H = SHA256(Prev_H || Timestamp || Actor || Action)
           |
           +---> [ Hyperledger Fabric Anchor ] ---> Ledger Tx with SHA-256 Digest
```

### 3.1 Non-Destructive Redaction Guarantee
- Redacting a document **NEVER** mutates or overwrites the original evidence.
- A new derived document version is created with its own independent UUID and independent SHA-256 digest.
- The parent relationship (`parent_document_id`) is permanently recorded.
- If `Hash(Original) == Hash(Redacted)`, the operation fails validation.

### 3.2 Forensic Watermark & Steganography
- Every exported file generates a unique `download_id`.
- The downloader's identity, role, client IP, User-Agent, and export timestamp are bundled into a `WatermarkPayload`.
- The payload is encrypted with AES-256-GCM using a server-managed secret and injected into the file following standard EOF markers.
- Verification via `POST /api/v1/admin/exports/verify-watermark` extracts this metadata to prove chain-of-custody without modifying file rendering.

---

## 4. Network Isolation & Boundary Controls

### 4.1 Subnet Isolation
- **Public Subnet:** Only the Application Load Balancer and NAT Gateways reside here.
- **Private Subnet:** ECS Fargate API and Worker tasks reside here with no direct internet ingress.
- **Data Subnet:** RDS, Redis, MinIO, and Fabric nodes reside in private subnets with **no route to the Internet** (neither IGW nor NAT Gateway).

### 4.2 Security Group Rules
- RDS (5432) accepts connections **only** from `evidentia-ecs-api-sg` and `evidentia-ecs-worker-sg`.
- Redis (6379) accepts connections **only** from `evidentia-ecs-api-sg` and `evidentia-ecs-worker-sg`.
- MinIO S3 API (9000) accepts connections **only** from `evidentia-ecs-api-sg` and `evidentia-ecs-worker-sg`.
- Fabric Peer gRPC (7051) accepts connections **only** from `evidentia-ecs-api-sg` and `evidentia-ecs-worker-sg`.
- **0.0.0.0/0 is strictly prohibited on all internal data ports.**

### 4.3 Database Administration via Adminer
- Adminer is strictly prohibited from being exposed to the public Internet.
- Administration is performed either via AWS Systems Manager (SSM) port forwarding or a private VPN:
```bash
aws ssm start-session \
  --target <INSTANCE_ID> \
  --document-name AWS-StartPortForwardingSessionToRemoteHost \
  --parameters '{"host":["evidentia-prod.xxxxxx.ap-south-1.rds.amazonaws.com"],"portNumber":["5432"],"localPortNumber":["5432"]}'
```

---

## 5. Web & API Security Controls

### 5.1 HTTP Security Headers
Every API response emits strict security headers via middleware (`internal/middleware/security_headers_middleware.go`):
- `Strict-Transport-Security: max-age=63072000; includeSubDomains; preload`
- `X-Content-Type-Options: nosniff`
- `X-Frame-Options: DENY`
- `Content-Security-Policy: default-src 'none'; frame-ancestors 'none'`
- `Referrer-Policy: strict-origin-when-cross-origin`

### 5.2 CORS Policy
- `CORS_ALLOWED_ORIGINS` must match the production frontend domain (`https://app.evidentia.example.com`).
- The wildcard `*` is explicitly blocked in production by startup configuration validation (`internal/config/validate.go`).

### 5.3 Brute Force & Rate Limiting
Login attempts are throttled by dual fixed-window Redis counters (`internal/ratelimit`):
- **IP Window:** Max 20 attempts per 15 minutes per client IP.
- **Account Window:** Max 10 attempts per 15 minutes per email address.
- Prevents distributed credential stuffing and targeted brute-force attacks.
