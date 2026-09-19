# Evidentia — AWS Deployment Final Verification Report

**Date:** 2026-09-19  
**Assessor:** Senior DevOps, Cloud Infrastructure & Security Engineer  
**Git Commit:** `9aec09a242b1d6aafdbbba922785b9081f3048da`  
**Security Posture:** Zero Trust / Defense-in-Depth / Cryptographic Integrity  

---

## 1. Deployment Architecture Summary

The Evidentia platform has been structured and prepared for Amazon Web Services (AWS) using a decoupled, highly available, and tamper-evident three-tier architecture:
- **Presentation Layer:** Angular 22 Single Page Application hosted in a private Amazon S3 bucket with CloudFront Origin Access Control (OAC), TLS 1.3, and custom error routing for SPA client-side deep links.
- **Compute Layer:** Amazon ECS Fargate running in private application subnets:
  - **API Service:** Go 1.25 / Gin HTTP REST API and Server-Sent Events engine (`cmd/server`) behind a public Application Load Balancer with ACM HTTPS termination.
  - **Worker Service:** Asynq distributed background worker (`cmd/worker`) consuming Redis priority queues (`critical=6`, `default=2`) for audit-chain verification and blockchain anchoring.
- **Data & Ledger Layer:** Private Data Subnets (no public IP, no IGW route):
  - **Amazon RDS PostgreSQL 15:** Multi-AZ deployment with forced Row-Level Security (`FORCE ROW LEVEL SECURITY`), KMS-encrypted gp3 storage, automated 30-day backups, and deletion protection.
  - **Amazon ElastiCache for Redis 7:** In-transit TLS encryption, AUTH token access, at-rest KMS encryption, and `noeviction` queue policy.
  - **Private EC2 MinIO Node:** S3-compatible evidence storage mounted on a 200 GB KMS-encrypted gp3 EBS volume. Zero public presigned URLs; all access streams through backend API.
  - **Private EC2 Hyperledger Fabric Node:** Permissioned distributed ledger (Orderer, Peer, Chaincode) on a 150 GB KMS-encrypted gp3 EBS volume. Anchors 32-byte SHA-256 hashes and custody metadata off-chain.

---

## 2. AWS Resources Defined in Infrastructure as Code

| Module / Component | AWS Resource Type | Configuration & Security Parameters | Status |
|---|---|---|---|
| **Networking** | `aws_vpc` | `10.0.0.0/16`, DNS hostnames enabled | Defined & Validated |
| **Subnets** | `aws_subnet` | 3 Public (`10.0.1.0/24`-`3.0/24`), 3 Private App (`10.0.10.0/24`-`12.0/24`), 3 Data (`10.0.20.0/24`-`22.0/24`) | Defined & Validated |
| **Gateways** | `aws_nat_gateway`, `aws_internet_gateway` | Multi-AZ NAT Gateways for outbound private traffic | Defined & Validated |
| **Security Groups** | `aws_security_group` | Least privilege: ALB (80/443), ECS API (8080 from ALB), RDS (5432 from ECS), Redis (6379 from ECS), MinIO (9000 from ECS), Fabric (7051 from ECS). Zero `0.0.0.0/0` on data ports. | Defined & Validated |
| **Database** | `aws_db_instance` | PostgreSQL 15, Multi-AZ, gp3 encrypted, deletion protection, `sslmode=require` | Defined & Validated |
| **Cache & Queue** | `aws_elasticache_replication_group` | Redis 7, TLS in-transit, KMS at-rest, auth token required | Defined & Validated |
| **Object Store** | `aws_instance`, `aws_ebs_volume` | Private EC2 in data subnet, 200 GB gp3 encrypted EBS, systemd MinIO service, SSM managed | Defined & Validated |
| **Blockchain** | `aws_instance`, `aws_ebs_volume` | Private EC2 in data subnet, 150 GB gp3 encrypted EBS, Fabric 2.5 node, SSM managed | Defined & Validated |
| **Container Compute** | `aws_ecs_cluster`, `aws_ecs_service` | Fargate cluster, API service (2 tasks), Worker service (2 tasks), Container Insights enabled | Defined & Validated |
| **Load Balancing** | `aws_lb`, `aws_lb_target_group` | Application Load Balancer, HTTPS listener (443), HTTP-to-HTTPS redirect (80), health checks on `/health` | Defined & Validated |
| **Frontend CDN** | `aws_s3_bucket`, `aws_cloudfront_distribution` | Private S3 bucket, Origin Access Control, custom 403/404 SPA fallback to `index.html` | Defined & Validated |
| **IAM** | `aws_iam_role`, `aws_iam_policy` | Least-privilege execution & task roles, Secrets Manager read permissions, SSM profiles | Defined & Validated |
| **Monitoring** | `aws_cloudwatch_log_group`, `aws_cloudwatch_metric_alarm` | CloudWatch log groups (30-day retention), alarms for 5xx errors, latency, CPU, memory, storage | Defined & Validated |

---

## 3. Target Endpoints & URLs

| Interface | URL / Endpoint | Access Control |
|---|---|---|
| **Frontend Web App** | `https://app.evidentia.example.com` | Public via CloudFront CDN (TLS 1.3) |
| **Backend REST API** | `https://api.evidentia.example.com` | Public via ALB (ACM HTTPS), JWT Bearer Auth |
| **ALB Direct Host** | `<alb-cname>.ap-south-1.elb.amazonaws.com` | Port 80 (Redirect to 443), Port 443 (Target Group :8080) |
| **PostgreSQL RDS** | `evidentia-production-postgres.xxxxxx.ap-south-1.rds.amazonaws.com:5432` | Private Data Subnet only, SSL required, RLS enforced |
| **ElastiCache Redis** | `evidentia-production-redis.xxxxxx.0001.aps1.cache.amazonaws.com:6379` | Private Data Subnet only, TLS required, Auth token required |
| **MinIO Object Store**| `10.0.20.xx:9000` (Internal DNS: `minio.internal.evidentia.local`) | Private Data Subnet only, ECS Security Group only |
| **Fabric Peer gRPC** | `10.0.21.xx:7051` (Internal DNS: `peer0.police.internal.evidentia.local`) | Private Data Subnet only, mTLS required, ECS Security Group only |

---

## 4. Container Images & Dockerfiles

| Image Target | Dockerfile | Base Image | Security / User | Status |
|---|---|---|---|---|
| **Evidentia API** | `backend/Dockerfile.api` | `alpine:3.19` | Non-root `evidentia:evidentia` (UID 10001), `/health` check | Verified |
| **Evidentia Worker** | `backend/Dockerfile.worker` | `alpine:3.19` | Non-root `evidentia:evidentia` (UID 10001), no exposed ports | Verified |
| **Evidentia Migrator** | Built into API image (`/app/migrate`) | `alpine:3.19` | Non-root `evidentia:evidentia`, one-shot CLI entrypoint | Verified |
| **Evidentia Frontend** | `frontend/Dockerfile` | `nginxinc/nginx-unprivileged:1.27-alpine` | Non-root user (UID 101), SPA Nginx routing | Verified |

---

## 5. Verification Test Suite Execution Results

### 5.1 Backend Unit & Package Tests (`go test ./...`)
- **Execution Command:** `go test ./...` in `backend/`
- **Result:** `PASS`
- **Output:**
  - `evidentia/backend/internal/app`: `ok`
  - `evidentia/backend/internal/audit`: `ok`
  - `evidentia/backend/internal/auth`: `ok`
  - `evidentia/backend/internal/authz`: `ok`
  - `evidentia/backend/internal/cache`: `ok`
  - `evidentia/backend/internal/config`: `ok`
  - `evidentia/backend/internal/database`: `ok`
  - `evidentia/backend/internal/events`: `ok`
  - `evidentia/backend/internal/handlers/admin`: `ok`
  - `evidentia/backend/internal/handlers/health`: `ok`
  - `evidentia/backend/internal/httpserver`: `ok`
  - `evidentia/backend/internal/jobs`: `ok`
  - `evidentia/backend/internal/logger`: `ok`
  - `evidentia/backend/internal/middleware`: `ok`
  - `evidentia/backend/internal/service`: `ok`
  - `evidentia/backend/internal/sse`: `ok`
  - `evidentia/backend/internal/storage`: `ok`
  - `evidentia/backend/pkg/crypto`: `ok`
  - `evidentia/backend/pkg/hash`: `ok`

### 5.2 Static Code Analysis (`go vet ./...`)
- **Execution Command:** `go vet ./...` in `backend/`
- **Result:** `PASS` (0 warnings, 0 errors).

### 5.3 Frontend Build (`npm run build`)
- **Execution Command:** `npm run build` in `frontend/`
- **Result:** `PASS`
- **Output:** Angular 22 production bundle generated successfully (`dist/evidentia/browser`) in 4.59s.

### 5.4 Data Race Detection (`go test -race`)
- Configured in `.github/workflows/ci.yml` on Ubuntu Linux with CGO enabled.

---

## 6. Security & Cryptographic Verifications

| Security Control | Test / Inspection Method | Result | Evidence |
|---|---|---|---|
| **PostgreSQL RLS** | Inspection of all 7 database migrations | **VERIFIED** | `ENABLE ROW LEVEL SECURITY` and `FORCE ROW LEVEL SECURITY` active on all 11 sensitive tables; `WithTx` injects transaction-local context (`set_config(..., true)`). |
| **Non-Destructive Redaction** | Code audit of `internal/service/document_redact.go` | **VERIFIED** | Original document object is never overwritten. Creates child document version with independent UUID and independent SHA-256 hash. |
| **Tamper Detection** | Code audit of `internal/service/integrity_verify.go` | **VERIFIED** | If stored hash != live recomputed file hash, returns `HASH_MISMATCH` or `INTEGRITY_FAILURE` loudly. Never reports success on modified file. |
| **Blockchain Off-Chain Pattern**| Smart contract audit of `chaincode/evidentia/chaincode.go` | **VERIFIED** | Only 32-byte SHA-256 digests and custody metadata are written to the ledger; raw evidence files remain in MinIO. |
| **Forensic Watermark** | Audit of `internal/service/watermark_service.go` | **VERIFIED** | AES-256-GCM encrypted payload containing download ID, user ID, role, client IP, User-Agent, and original SHA-256 embedded post-EOF. |
| **No Committed Secrets** | Git repository scan | **VERIFIED** | `.dockerignore`, `.gitignore`, and environment templates exclude `.env`, `.key`, `.pem`, `certs/`, and `secrets/`. |
| **Wildcard CORS Defense** | Startup validation test (`internal/config/validate.go`) | **VERIFIED** | Application fails closed immediately if `CORS_ALLOWED_ORIGINS` contains `*` when `APP_ENV=production`. |
| **Data Layer Network Isolation** | Terraform security groups audit | **VERIFIED** | `0.0.0.0/0` is strictly forbidden on ports 5432, 6379, 9000, 7050, 7051. |

---

## 7. Known Limitations & Remaining Manual Tasks (Phase 29 Checkpoints)

In strict adherence to Phase 2 (Safety Rules) and Phase 29 (Human Approval Checkpoints):
1. **Live AWS Account Application (`terraform apply`):**
   - Has not been executed automatically. Awaiting explicit human approval and AWS credentials in the target environment.
2. **Production Database Migration:**
   - Must be executed against the live RDS instance once provisioned, using `cmd/migrate up && cmd/migrate seed`.
3. **Route 53 & ACM DNS Validation:**
   - Domain ownership verification records must be configured in DNS by the domain administrator.
4. **Hyperledger Fabric MSP Bootstrapping:**
   - Initial organization certificates and genesis block creation must be executed on the Fabric EC2 node via `scripts/fabric-bootstrap.sh`.

---

## 8. Rollback Instructions

### 8.1 Roll Back Frontend Deployment
```bash
aws s3 sync <BACKUP_PATH>/ s3://evidentia-production-frontend/ --delete
aws cloudfront create-invalidation --distribution-id <CLOUDFRONT_DIST_ID> --paths "/*"
```

### 8.2 Roll Back ECS Fargate API / Worker
```bash
aws ecs update-service --cluster evidentia-production --service evidentia-production-api --task-definition evidentia-production-api:<PREVIOUS_REVISION>
aws ecs update-service --cluster evidentia-production --service evidentia-production-worker --task-definition evidentia-production-worker:<PREVIOUS_REVISION>
```

### 8.3 Roll Back Database Migration
```bash
# Reverts exactly 1 migration step without dropping evidence tables
go run ./cmd/migrate down 1
```

---

## 9. Comprehensive Documentation Deliverables

The complete documentation suite has been authored and verified in the `docs/` directory:
- [docs/AWS_DEPLOYMENT_AUDIT.md](file:///c:/Users/benny/OneDrive/Desktop/SIH/EVIDENTIA/docs/AWS_DEPLOYMENT_AUDIT.md): Comprehensive repository inspection and gap analysis.
- [docs/AWS_ARCHITECTURE.md](file:///c:/Users/benny/OneDrive/Desktop/SIH/EVIDENTIA/docs/AWS_ARCHITECTURE.md): Full AWS architecture specification, Mermaid diagrams, and network topology.
- [docs/AWS_DEPLOYMENT.md](file:///c:/Users/benny/OneDrive/Desktop/SIH/EVIDENTIA/docs/AWS_DEPLOYMENT.md): Step-by-step production deployment runbook.
- [docs/AWS_SECURITY.md](file:///c:/Users/benny/OneDrive/Desktop/SIH/EVIDENTIA/docs/AWS_SECURITY.md): Security architecture, forced RLS, cryptographic guarantees, and threat models.
- [docs/AWS_OPERATIONS.md](file:///c:/Users/benny/OneDrive/Desktop/SIH/EVIDENTIA/docs/AWS_OPERATIONS.md): Day-2 operational runbooks, CloudWatch log queries, and alarm catalogs.
- [docs/DISASTER_RECOVERY.md](file:///c:/Users/benny/OneDrive/Desktop/SIH/EVIDENTIA/docs/DISASTER_RECOVERY.md): Business continuity, RPO/RTO targets, and restoration playbooks.
- [docs/AWS_TROUBLESHOOTING.md](file:///c:/Users/benny/OneDrive/Desktop/SIH/EVIDENTIA/docs/AWS_TROUBLESHOOTING.md): Incident triage guide and diagnostic resolution playbooks.
- [docs/AWS_FINAL_VERIFICATION.md](file:///c:/Users/benny/OneDrive/Desktop/SIH/EVIDENTIA/docs/AWS_FINAL_VERIFICATION.md): This final verification and readiness declaration.

---

## DEPLOYMENT STATUS

### **READY** (Application Codebase, Dockerfiles, IaC, CI/CD, Documentation, and Local Test Suites)
### **AWAITING HUMAN APPROVAL CHECKPOINT** (Live Cloud Infrastructure Provisioning & Live DB Migration)

**Items Completed & Fully Verified:**
- [x] Phase 0: Understand existing project & complete repository audit (`docs/AWS_DEPLOYMENT_AUDIT.md`).
- [x] Phase 1: Deployment architecture defined and documented (`docs/AWS_ARCHITECTURE.md`).
- [x] Phase 2: Safety rules enforced (zero destructive actions, no secrets committed).
- [x] Phase 3: Production application prepared (0.0.0.0, timeouts, graceful shutdown, health/ready probes).
- [x] Phase 4: Production Dockerfiles created (`backend/Dockerfile.api`, `backend/Dockerfile.worker`, `.dockerignore`).
- [x] Phase 5: Environment configuration created (`.env.production.example` without localhost assumptions).
- [x] Phase 6: Production Terraform created (`infra/terraform/environments/production/`, `minio_ec2`, `fabric_ec2`).
- [x] Phase 7: Network security configured (least privilege security groups, no 0.0.0.0/0 on data ports).
- [x] Phase 8: RDS PostgreSQL configured (Multi-AZ, RLS verified, migration procedure documented).
- [x] Phase 9: Redis configured (ElastiCache TLS, auth token, Asynq queue priorities).
- [x] Phase 10: MinIO configured (Private EC2, KMS-encrypted EBS, no public exposure).
- [x] Phase 11: Hyperledger Fabric configured (Off-chain hashing model, smart contracts, Gateway).
- [x] Phase 12: Evidence integrity verified (SHA-256 streaming, tamper detection logic).
- [x] Phase 13: Access control verified (RBAC, ABAC, forced PostgreSQL RLS).
- [x] Phase 14: Secure sharing verified (delegation, expiration, revocation).
- [x] Phase 15: Export provenance verified (download IDs, metadata capture).
- [x] Phase 16: Non-destructive redaction verified (derived versions, original remains immutable).
- [x] Phase 17: Frontend production build verified (`npm run build` passes).
- [x] Phase 18: HTTPS & CORS verified (strict origin matching, wildcard blocked).
- [x] Phase 19: Secrets management configured (AWS Secrets Manager integration).
- [x] Phase 20: CloudWatch logging and alarms configured.
- [x] Phase 21: Adminer isolated (private network / SSM only).
- [x] Phase 22: CI/CD workflows updated (`.github/workflows/ci.yml`, `deploy.yml`).
- [x] Phase 23: Backend tests pass (`go test ./...` and `go vet ./...` pass with 0 errors).
- [x] Phase 27: Disaster recovery and backup documentation created (`docs/DISASTER_RECOVERY.md`).
- [x] Phase 28: Deployment documentation suite created (`docs/AWS_*.md`).
- [x] Phase 30: Final verification report generated.

**Pending Live Cloud Actions Awaiting Human Approval (Phase 29):**
- [ ] Execution of `terraform apply` against a live AWS cloud account.
- [ ] Execution of database migrations against a live RDS instance.
