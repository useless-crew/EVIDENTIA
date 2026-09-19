# Evidentia — AWS Troubleshooting & Incident Diagnostic Runbook

This guide provides diagnostic procedures, triage steps, and resolution runbooks for common issues across the Evidentia AWS platform.

---

## 1. Quick Diagnostic Triage

### 1.1 Verify Infrastructure Endpoints
```bash
# 1. Check process liveness
curl -is https://api.evidentia.example.com/health

# 2. Check dependency readiness (Postgres, Redis, MinIO)
curl -is https://api.evidentia.example.com/ready

# 3. Check admin system health (requires ADMIN JWT)
curl -is -H "Authorization: Bearer $ADMIN_TOKEN" https://api.evidentia.example.com/api/v1/admin/system/health
```

---

## 2. Common Issues & Resolution Playbooks

### Issue 1: API Container Fails to Start (CrashLoopBackOff)
* **Symptom:** ECS task restarts repeatedly; CloudWatch log shows: `startup failed: ...`
* **Common Root Causes & Fixes:**
  1. **Wildcard CORS in Production:**
     - *Error Log:* `CORS_ALLOWED_ORIGINS must not include "*" in production`
     - *Cause:* `APP_ENV=production` is set, but `CORS_ALLOWED_ORIGINS` contains `*`.
     - *Fix:* Set `CORS_ALLOWED_ORIGINS` to the exact frontend origin (e.g. `https://app.evidentia.example.com`).
  2. **Unreachable Database:**
     - *Error Log:* `database: ping: dial tcp ...:5432: i/o timeout`
     - *Cause:* Security group misconfiguration or RDS is not in the same VPC/subnets.
     - *Fix:* Ensure `evidentia-rds-sg` allows port 5432 inbound from `evidentia-ecs-api-sg`.
  3. **Missing or Short JWT Secret:**
     - *Error Log:* `JWT_SIGNING_KEY must be at least 32 characters`
     - *Cause:* Secret in Secrets Manager is empty or shorter than 32 chars.
     - *Fix:* Update secret in Secrets Manager with `openssl rand -base64 48`.

---

### Issue 2: Database Connection Pool Exhaustion (HTTP 500)
* **Symptom:** Sudden spike in HTTP 500 errors; logs show: `timeout acquiring connection from pool` or `pgx: max connections reached`.
* **Diagnostic Commands:**
  - Connect to RDS via SSM session:
```sql
SELECT count(*), state FROM pg_stat_activity GROUP BY state;
SELECT pid, query_start, now() - query_start AS duration, query 
FROM pg_stat_activity 
WHERE state = 'active' ORDER BY duration DESC LIMIT 10;
```
* **Resolution:**
  1. Terminate runaway idle-in-transaction connections:
```sql
SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE state = 'idle in transaction' AND now() - state_change > interval '5 minutes';
```
  2. If traffic increased legitimately, increase `DATABASE_MAX_OPEN_CONNS` from 50 to 100 in task definition and ensure RDS `max_connections` parameter is sufficient.

---

### Issue 3: PostgreSQL RLS Authorization Denials (HTTP 403 / Missing Rows)
* **Symptom:** User is authenticated and possesses proper RBAC role, but query returns empty results or permission denied.
* **Root Cause Analysis:**
  - Evidentia enforces defense-in-depth: Even if application RBAC permits an action, PostgreSQL RLS checks `current_app_user_id()` and `current_app_role()`.
  - For case access: User must either be the case creator (`created_by`), an active member in `case_members` (`removed_at IS NULL`), or have role `ADMIN`.
* **Resolution:**
  1. Inspect whether user is an active member of the case:
```sql
SELECT * FROM case_members WHERE case_id = '<CASE_ID>' AND user_id = '<USER_ID>';
```
  2. If `removed_at` is not null, membership was revoked.
  3. If user was never added, an authorized police officer or admin must add them via `POST /api/v1/cases/:id/members`.

---

### Issue 4: Redis Connection & Asynq Worker Stalls
* **Symptom:** Background jobs (audit verification or blockchain anchoring) stay in `PENDING` state indefinitely.
* **Diagnostic Steps:**
  1. Check ElastiCache Redis memory & connection metrics in CloudWatch.
  2. Verify Worker service is running in ECS:
```bash
aws ecs describe-services --cluster evidentia-production --services evidentia-production-worker \
  --query "services[0].[runningCount,desiredCount]"
```
  3. Check Worker container logs in `/ecs/evidentia-production/worker`:
```sql
fields @timestamp, msg, error
| filter level = "ERROR"
| sort @timestamp desc
| limit 20
```
  4. If logs show `NOAUTH Authentication required`: Ensure `REDIS_PASSWORD` matches the ElastiCache auth token.
  5. If logs show `x509: certificate signed by unknown authority`: Ensure `REDIS_TLS=true` is set properly for ElastiCache TLS.

---

### Issue 5: MinIO Object Storage Upload Failures
* **Symptom:** Document upload returns HTTP 500; logs show: `storage: put object: ...`
* **Diagnostic Steps:**
  1. Verify MinIO process status via SSM:
```bash
aws ssm send-command \
  --instance-ids <MINIO_INSTANCE_ID> \
  --document-name "AWS-RunShellScript" \
  --parameters 'commands=["systemctl status minio", "df -h /data"]'
```
  2. If disk is full (`100% used on /data`): Expand EBS volume per `docs/AWS_OPERATIONS.md`.
  3. Verify bucket exists:
```bash
/usr/local/bin/mc ls local/
```
  4. If bucket is missing, recreate: `/usr/local/bin/mc mb local/evidentia-documents`.

---

### Issue 6: Hyperledger Fabric Gateway & Anchoring Failures
* **Symptom:** Blockchain anchor tasks fail; status in `blockchain_anchors` is `FAILED`.
* **Error Analysis:**
  - `blockchain: Hyperledger Fabric network is unavailable`: Peer is unreachable or gRPC connection timed out.
  - *Distinction:* `BLOCKCHAIN_UNAVAILABLE` indicates an infrastructure issue. The document is **NOT** marked as tampered.
  - `blockchain: hash on ledger does not match`: Indicates genuine blockchain digest discrepancy (`BLOCKCHAIN_MISMATCH`).
* **Resolution:**
  1. Inspect Fabric peer container logs on the Fabric EC2 instance:
```bash
docker logs --tail 100 peer0.police.evidentia.local
```
  2. Verify peer gRPC endpoint is responding:
```bash
nc -zv <FABRIC_PRIVATE_IP> 7051
```
  3. Verify MSP certificates are valid and not expired:
```bash
openssl x509 -in /etc/evidentia/fabric/client.crt -text -noout | grep "Not After"
```

---

### Issue 7: Server-Sent Events (SSE) Stream Disconnects
* **Symptom:** Real-time audit progress or case updates fail to stream to the browser or buffer until completion.
* **Root Cause & Fix:**
  - ALB or intermediate reverse proxy is buffering responses.
  - Ensure the response headers include:
    - `Content-Type: text/event-stream`
    - `Cache-Control: no-cache`
    - `Connection: keep-alive`
    - `X-Accel-Buffering: no`
  - Ensure ALB idle timeout is set to at least `60s` (the server sends keep-alive comments every 15s).

---

### Issue 8: Forensic Watermark Verification Fails (`verify-watermark`)
* **Symptom:** `POST /api/v1/admin/exports/verify-watermark` returns: `no forensic watermark found` or `watermark decryption failed`.
* **Root Cause & Fix:**
  1. **File Alteration in Transit:** If an intermediate proxy or email client re-encoded the image (e.g. compressing JPEG or stripping metadata), the appended EOF block was stripped.
  2. **Mismatched Server Secret:** Watermark payload encryption uses `signingKey` derived from `JWT_SIGNING_KEY`. If the file was exported on an environment with Key A and uploaded to an environment with Key B, decryption fails. Verify that the file originates from the same deployment environment.
