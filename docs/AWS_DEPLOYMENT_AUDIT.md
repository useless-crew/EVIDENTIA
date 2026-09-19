# Evidentia — AWS Deployment Audit & Readiness Assessment

**Date:** 2026-09-19  
**Assessor:** Senior DevOps, Cloud Infrastructure & Security Engineer  
**Scope:** Complete repository inspection (Backend, Frontend, Blockchain/Chaincode, Storage, Network, Infrastructure as Code, CI/CD)  
**Security & Integrity Posture:** Zero Trust, Evidence Integrity First, Defense-in-Depth

---

## Executive Summary

Evidentia is an enterprise digital evidence and case-management platform designed for law enforcement, forensic laboratories, prosecution/defense counsel, and the judiciary. The application enforces strong cryptographic guarantees:
- SHA-256 evidence hashing upon upload with streaming chunk verification.
- Cryptographic append-only audit trail with recursive SHA-256 hash chaining.
- Multi-party blockchain anchoring via Hyperledger Fabric 2.5 LTS (storing metadata and cryptographic digests off-chain).
- Strict dual-layer access control: Application-level RBAC + ABAC combined with database-level PostgreSQL Row-Level Security (RLS) with `FORCE ROW LEVEL SECURITY`.
- Non-destructive redaction producing separate derivative evidence versions.
- Forensic export provenance tracking with AES-256-GCM invisible steganographic watermarking.

This audit evaluates the current codebase against production readiness requirements for deployment on Amazon Web Services (AWS).

---

## 1. Current Architecture

### 1.1 High-Level Flow
```
                                 [ Client Browser / SPA ]
                                             |
                   +-------------------------+-------------------------+
                   | (Static Assets / HTTPS)                           | (API Requests / HTTPS)
                   v                                                   v
         [ CloudFront CDN ]                                 [ Application Load Balancer ]
                   |                                                   |
                   v                                                   v
          [ S3 Web Bucket ]                                    [ ECS Fargate API ]
         (Angular 22 SPA)                                      (Gin REST + SSE)
                                                                       |
                         +--------------------+------------------------+------------------------+
                         |                    |                        |                        |
                         v                    v                        v                        v
                [ RDS PostgreSQL 15 ]   [ ElastiCache Redis 7 ]  [ Private EC2 MinIO ]   [ Private EC2 Fabric ]
                 - RLS Enforced          - Asynq Task Queues      - Encrypted EBS Vol     - Orderer & Peers
                 - Audit Log Chains      - SSE Pub/Sub Engine     - Evidence Objects      - Chaincode Go
                 - Metadata & Auth       - Dual Rate Limiter      - No Presigned URLs     - Fabric Gateway gRPC
                         ^                    ^
                         |                    |
                         +-- [ ECS Fargate Worker ] (Asynq Consumer: Audit & Blockchain Tasks)
```

### 1.2 Processing Topology
1. **API Tier:** Dual-role capability (`cmd/server` can run embedded Asynq worker for dev, or disable it via `DISABLE_EMBEDDED_WORKER=true` in production).
2. **Worker Tier:** Standalone background worker (`cmd/worker`) consumes Asynq priority queues (`critical=6`, `default=2`) for long-running audit verification and blockchain anchoring.
3. **Storage Tier:** Off-chain object storage (MinIO) paired with relational metadata (RDS PostgreSQL) and permissioned distributed ledger (Hyperledger Fabric).
4. **Data Isolation:** PostgreSQL connection pool executes all queries wrapped in `repository.WithTx`, injecting `app.user_id` and `app.role` via transaction-scoped `set_config(..., true)`.

---

## 2. Current Components & Frameworks

| Component | Framework / Technology | Version | Location / Entrypoint |
|---|---|---|---|
| **Frontend** | Angular (Standalone Components) | `22.1.0` | `frontend/src/` |
| **Frontend Runtime** | Nginx Unprivileged / Node build | `nginxinc/nginx-unprivileged:1.27-alpine` | `frontend/Dockerfile` |
| **Backend API** | Go / Gin Web Framework | Go `1.25.0`, Gin `v1.11.0` | `backend/cmd/server/main.go` |
| **Background Worker** | Go / Asynq | `v0.25.1` | `backend/cmd/worker/main.go` |
| **Database Migrator** | Go / golang-migrate | `v4.18.2` | `backend/cmd/migrate/main.go` |
| **Primary Database** | PostgreSQL | `15` | `backend/db/migrations/` |
| **Cache & Queue** | Redis | `7` | `internal/cache`, `internal/jobs` |
| **Object Store** | MinIO (S3-Compatible) | `latest` (minio-go `v7.0.87`) | `internal/storage` |
| **Blockchain** | Hyperledger Fabric | `2.5 LTS` | `fabric/`, `chaincode/evidentia` |
| **Fabric Gateway** | fabric-gateway Go SDK | `v1.7.1` | `internal/blockchain/fabric.go` |
| **Reverse Proxy** | Nginx Template | `1.27-alpine` | `ops/reverse-proxy/` |
| **IaC** | HashiCorp Terraform | `>= 1.5` | `infra/terraform/` |

---

## 3. Current Dependencies

### Backend (`backend/go.mod`)
- `github.com/gin-gonic/gin v1.11.0`: HTTP router and middleware engine.
- `github.com/jackc/pgx/v5 v5.7.2`: High-performance PostgreSQL driver and connection pooling (`pgxpool`).
- `github.com/golang-jwt/jwt/v5 v5.2.1`: HMAC-SHA256 JWT claims signing and validation.
- `golang.org/x/crypto v0.35.0`: bcrypt password hashing.
- `github.com/redis/go-redis/v9 v9.7.1`: Redis client for caching, rate limiting, and pub/sub.
- `github.com/hibiken/asynq v0.25.1`: Distributed task queue backed by Redis.
- `github.com/minio/minio-go/v7 v7.0.87`: S3-compatible object storage SDK.
- `github.com/hyperledger/fabric-gateway v1.7.1`: Official Fabric Gateway client SDK.
- `github.com/golang-migrate/migrate/v4 v4.18.2`: Database migration runner.
- `github.com/google/uuid v1.6.0`: UUID generation and parsing.
- `github.com/joho/godotenv v1.5.1`: Local environment loader.
- `github.com/stretchr/testify v1.10.0`: Unit and integration testing assertions.

### Frontend (`frontend/package.json`)
- `@angular/core`, `@angular/common`, `@angular/router`, `@angular/forms`: `^22.1.0`
- `rxjs`: `~7.8.0`
- `gsap`: `^3.15.0`
- `lenis`: `^1.3.26`
- Dev: `@angular/cli ^22.1.6`, `typescript ~6.0.2`, `vitest ^4.0.8`, `prettier ^3.8.1`

---

## 4. Workflows & Subsystems Audit

### 4.1 Authentication & Session Security
- **Mechanism:** JWT bearer tokens (HS256) + server-side refresh token sessions (`auth_sessions` table).
- **Brute Force Protection:** Dual fixed-window Redis rate limiter (`LOGIN_RATE_LIMIT_IP_MAX=20/15m`, `LOGIN_RATE_LIMIT_ACCOUNT_MAX=10/15m`).
- **Initial Bootstrap:** Secure one-time bootstrap admin creation via environment variables (`EVIDENTIA_BOOTSTRAP_ADMIN_EMAIL/_PASSWORD/_NAME`). Unset by default; fails closed if partially configured.

### 4.2 Authorization: RBAC & ABAC
- **RBAC:** Fixed catalog of roles (`ADMIN`, `POLICE`, `FORENSICS`, `LAWYER`, `JUDGE`) and granular permissions (`case:create`, `document:upload`, `document:download`, `document:redact`, `audit:verify`, etc.).
- **ABAC:** Dynamic case-membership checks (`case_members`), ownership checks (`created_by`), and document sharing delegation (`document_shares`).
- **Defense-in-Depth:** PostgreSQL RLS (`ENABLE ROW LEVEL SECURITY` and `FORCE ROW LEVEL SECURITY`) enforces boundaries even if application logic were to fail.

### 4.3 Evidence Lifecycle & Cryptographic Integrity
1. **Upload:** Streaming chunk-based SHA-256 hash calculation directly during multipart upload (`pkg/hash`). Raw bytes written to MinIO.
2. **Metadata:** Document record inserted into PostgreSQL with 32-byte binary digest (`BYTEA`).
3. **Audit Entry:** Cryptographic audit entry chained to previous log entry hash.
4. **Blockchain Anchor:** Asynq task queued to submit `AnchorEvidence` transaction to Hyperledger Fabric.
5. **Redaction:** Strictly creates a derived child document version with independent SHA-256 hash. The original document is never mutated or overwritten.
6. **Export & Watermarking:** Forensic export generates a unique download ID, captures client IP and User-Agent, generates an export fingerprint, and embeds an AES-256-GCM encrypted invisible steganographic watermark into JPEG, PNG, or PDF files.

---

## 5. Vulnerabilities, Gaps & Localhost Dependencies

### 5.1 Localhost Dependencies
1. **Frontend Environment:**
   - `frontend/src/environments/environment.development.ts` points to `http://localhost:8080/api/v1`.
   - `frontend/src/environments/environment.ts` uses relative `/api/v1`. If backend is hosted on a separate subdomain (`api.evidentia.<domain>`), cross-origin routing must be configured or CloudFront must forward `/api/*` to the ALB.
2. **Backend Config Defaults:**
   - Defaults in `internal/config/config.go`:
     - `DATABASE_HOST` -> `localhost`
     - `REDIS_ADDR` -> `localhost:6379`
     - `MINIO_ENDPOINT` -> `localhost:9000`
     - `CORS_ALLOWED_ORIGINS` -> `http://localhost:4200`

### 5.2 Hardcoded Credentials & Placeholders
1. **Docker Compose:**
   - `docker-compose.yml` and `docker-compose.prod.yml` contain fallback placeholders (`changeme_example`, `evidentia_minio`, etc.).
   - While `internal/config` validates required credentials at startup, Terraform configurations and production environments must source credentials exclusively from AWS Secrets Manager.
2. **Demo Terraform Environment (`infra/terraform/environments/demo/main.tf`):**
   - Lines 198–203 improperly map `DATABASE_PASSWORD` and `REDIS_PASSWORD` to `var.jwt_secret_arn` / `var.minio_secret_arn`.
   - Line 191 sets `CORS_ALLOWED_ORIGINS` to `*` if `var.domain_name` is empty. Because `APP_ENV=production`, `validate.go` explicitly aborts startup on wildcard CORS in production!

### 5.3 Insecure Configurations
1. **Adminer:** Exposed on port 8088 in local compose override. Must never be exposed to the public Internet in AWS. Access must be restricted to private SSM Session Manager or VPN tunnels.
2. **Missing Production Terraform Modules:**
   - No `production` environment exists in `infra/terraform/environments/` (only `demo/`).
   - No Terraform module exists for the private EC2 MinIO instance with encrypted EBS storage.
   - No Terraform module exists for the private EC2 Hyperledger Fabric cluster.
   - ALB is currently bundled inside the `ecs` module rather than cleanly decoupled.

### 5.4 Health Checks & Observability Gaps
1. **HTTP Endpoints:**
   - `GET /health` (liveness) and `GET /ready` (readiness for Postgres, Redis, MinIO) exist and are robust.
   - However, `GET /ready` does not currently reflect Hyperledger Fabric readiness status when `FABRIC_ENABLED=true`.
2. **Worker Container:**
   - The standalone Asynq worker (`cmd/worker`) has no HTTP listener, so HTTP-based health checks fail if applied to it. An internal process/queue inspection mechanism is needed for ECS Fargate health.

---

## 6. AWS Target Architecture Specifications

### 6.1 Network & Subnet Topology
```
VPC: 10.0.0.0/16
├── Public Subnets (10.0.1.0/24, 10.0.2.0/24)
│   ├── Internet Gateway
│   ├── NAT Gateways (Multi-AZ in production)
│   └── Application Load Balancer (ALB)
├── Private Subnets (10.0.10.0/24, 10.0.11.0/24)
│   ├── ECS Fargate API Service
│   └── ECS Fargate Worker Service
└── Data Subnets (10.0.20.0/24, 10.0.21.0/24) - Isolated, No Internet Inbound
    ├── Amazon RDS PostgreSQL 15 (Multi-AZ)
    ├── Amazon ElastiCache Redis 7 (In-transit encryption enabled)
    ├── Private EC2 MinIO (Encrypted gp3 EBS volume)
    └── Private EC2 Hyperledger Fabric Node (Encrypted gp3 EBS volume)
```

### 6.2 Security Groups & Least Privilege Matrix
| Security Group | Source / Inbound | Destination / Outbound | Ports | Protocol |
|---|---|---|---|---|
| **ALB-SG** | `0.0.0.0/0` (HTTPS) | ECS-API-SG | In: `443`, Out: `8080` | TCP |
| **ECS-API-SG** | ALB-SG | RDS-SG, Redis-SG, MinIO-SG, Fabric-SG | In: `8080`, Out: `5432, 6379, 9000, 7051` | TCP |
| **ECS-Worker-SG** | None (No Inbound) | RDS-SG, Redis-SG, MinIO-SG, Fabric-SG | Out: `5432, 6379, 9000, 7051` | TCP |
| **RDS-SG** | ECS-API-SG, ECS-Worker-SG | None | In: `5432` | TCP |
| **Redis-SG** | ECS-API-SG, ECS-Worker-SG | None | In: `6379` | TCP |
| **MinIO-SG** | ECS-API-SG, ECS-Worker-SG | None | In: `9000` (S3 API) | TCP |
| **Fabric-SG** | ECS-API-SG, ECS-Worker-SG | None | In: `7051` (Peer gRPC) | TCP |

*Zero public access is permitted to RDS, Redis, MinIO, or Fabric.*

---

## 7. Service-Specific Deployment Requirements

### 7.1 Amazon RDS PostgreSQL
- Engine: PostgreSQL 15.x.
- Storage: gp3, encrypted with AWS KMS.
- Availability: Multi-AZ enabled for production.
- Access: In private data subnet group only; `publicly_accessible = false`.
- Parameters: `rds.force_ssl = 1`.
- Migration Procedure: Run via one-shot ECS task or controlled CI/CD runner executing `/app/migrate up && /app/migrate seed` using the `evidentia_migrator` administrative user before rolling out updated API tasks.

### 7.2 AWS ElastiCache for Redis
- Engine: Redis 7.x compatible.
- In-Transit Encryption: TLS enabled (`REDIS_TLS=true`).
- Access Control: Redis AUTH token managed via Secrets Manager.
- Networking: Deployed in private data subnet group; no public access.

### 7.3 MinIO Object Storage on Private EC2
- Instance: Private EC2 instance (`t3.medium` or `m6i.large`) in private data subnet.
- Storage: Dedicated EBS `gp3` volume formatted as XFS/ext4, encrypted with KMS, mounted at `/data`.
- Network: No public IP; accessible only via internal security group on port 9000.
- Operations: Managed systemd service running `minio server /data`. Automated EBS snapshots for point-in-time disaster recovery.

### 7.4 Hyperledger Fabric on Private Infrastructure
- Deployment: Private EC2 instance running Orderer, Peer, and Chaincode containers via Docker or dedicated binary services.
- Data Volume: Persistent encrypted EBS volume for ledger state and CouchDB/LevelDB.
- Security: Peer gRPC port 7051 secured with mTLS. Admin and Operations endpoints strictly bound to localhost or private administrative interfaces.
- Key Management: MSP identity certificates and private keys generated during bootstrap, mounted securely, and never committed to version control.

---

## 8. AWS Deployment Blockers & Remediation Roadmap

| Blocker / Gap | Impact | Remediation Step |
|---|---|---|
| **1. Missing `production` Terraform environment** | Cannot deploy production infrastructure via IaC | Create `infra/terraform/environments/production/` with modular configs and `terraform.tfvars.example` |
| **2. Missing MinIO & Fabric Terraform modules** | Cannot provision private EC2 storage & blockchain nodes | Create dedicated Terraform modules for MinIO EC2 and Fabric EC2 with encrypted EBS |
| **3. Invalid CORS wildcard logic in IaC** | Backend fails closed upon startup in production | Configure specific domain names in Terraform variables and `.env.production.example` |
| **4. Dockerfile structure** | Prompt requires dedicated `Dockerfile.api` & `Dockerfile.worker` | Create `Dockerfile.api` and `Dockerfile.worker` alongside multi-stage builds |
| **5. Missing environment examples** | Incomplete deployment configuration guidance | Create `.env.example` and `.env.production.example` without real secrets |
| **6. Health and readiness visibility** | Fabric health not tracked in `/ready` probe | Update readiness handler to report Fabric status when enabled without breaking fallback |
| **7. Production documentation** | Ops procedures not centralized | Author `docs/AWS_ARCHITECTURE.md`, `docs/AWS_DEPLOYMENT.md`, `docs/AWS_SECURITY.md`, `docs/AWS_OPERATIONS.md`, `docs/DISASTER_RECOVERY.md`, `docs/AWS_TROUBLESHOOTING.md` |

---

## Conclusion

The Evidentia platform features exceptional security and integrity foundations (SHA-256 streaming hashing, forced RLS, cryptographic audit chaining, steganographic watermarking, and Fabric smart contracts). Resolving the infrastructure as code gaps, creating dedicated production Dockerfiles, parameterizing secrets, and securing the network perimeter will ensure seamless, hardened deployment to AWS.
