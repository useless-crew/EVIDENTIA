# Evidentia — AWS Operations, Monitoring & Observability Guide

This document outlines day-2 operational runbooks, CloudWatch monitoring, alarm configurations, log queries, and scaling procedures.

---

## 1. Observability Architecture

Evidentia emits structured JSON operational logs to standard output. In AWS ECS Fargate, these logs are routed directly to Amazon CloudWatch via the `awslogs` driver with 30-day retention.

```
[ ECS API / Worker Containers ] 
           | (JSON stdout)
           v
[ Amazon CloudWatch Log Groups ]
   - /ecs/evidentia-production/api
   - /ecs/evidentia-production/worker
           |
           +---> [ CloudWatch Metric Filters ] ---> [ CloudWatch Alarms ] ---> [ SNS Notifications ]
           |
           +---> [ CloudWatch Insights Queries ] (Diagnostic Triage)
```

---

## 2. CloudWatch Alarms & Alert Thresholds

| Alarm Name | Metric / Source | Condition / Threshold | Evaluation Period | Severity | Action |
|---|---|---|---|---|---|
| `evidentia-prod-api-5xx-rate` | ALB `HTTPCode_Target_5XX_Count` | `>= 5` errors | 2 out of 2 periods (1 min) | High | PagerDuty / On-call notification |
| `evidentia-prod-api-high-latency` | ALB `TargetResponseTime` | `>= 1.5s` average | 3 consecutive periods (1 min) | Medium | Check DB query contention / Scale API |
| `evidentia-prod-ecs-api-cpu` | ECS `CPUUtilization` (API) | `>= 80%` | 3 consecutive periods (5 min) | Medium | Auto-scale API task count |
| `evidentia-prod-ecs-api-memory` | ECS `MemoryUtilization` (API) | `>= 85%` | 2 consecutive periods (5 min) | High | Potential leak; inspect memory stats |
| `evidentia-prod-ecs-worker-cpu` | ECS `CPUUtilization` (Worker) | `>= 85%` | 3 consecutive periods (5 min) | Medium | Auto-scale worker task count |
| `evidentia-prod-rds-cpu` | RDS `CPUUtilization` | `>= 80%` | 2 consecutive periods (5 min) | High | Inspect heavy queries / slow logs |
| `evidentia-prod-rds-storage-low` | RDS `FreeStorageSpace` | `<= 15 GB` | 1 period (5 min) | High | Increase allocated storage |
| `evidentia-prod-minio-disk-full` | CloudWatch Agent `disk_used_percent` | `>= 85%` | 1 period (5 min) | High | Expand EBS volume size |
| `evidentia-prod-blockchain-failures`| Metric Filter: `"blockchain: AnchorEvidence failed"` | `>= 1` occurrence | 1 period (1 min) | Critical | Inspect Fabric peer status / mTLS certs |

---

## 3. Operational Log Filtering & CloudWatch Insights

Evidentia logs all events in structured JSON format with `level`, `msg`, `time`, `request_id`, and contextual metadata.

### 3.1 Investigate API Errors (5xx)
```sql
fields @timestamp, msg, error, path, method, status, request_id
| filter level = "ERROR" and status >= 500
| sort @timestamp desc
| limit 50
```

### 3.2 Track Blockchain Anchoring Transactions
```sql
fields @timestamp, msg, evidence_id, tx_id, status, error
| filter msg like /blockchain: AnchorEvidence/
| sort @timestamp desc
| limit 100
```

### 3.3 Monitor Failed Login Attempts & Brute Force
```sql
fields @timestamp, msg, email, ip, reason
| filter msg = "authentication failed" or msg = "login rate limit exceeded"
| stats count(*) as failures by ip, email
| sort failures desc
| limit 20
```

### 3.4 Audit Chain Verification Activity
```sql
fields @timestamp, msg, verification_id, status, entries_checked
| filter msg like /audit verification/
| sort @timestamp desc
| limit 50
```

---

## 4. Routine Maintenance Runbooks

### 4.1 Scaling ECS Fargate Services
```bash
# Scale API service during peak court/police hours
aws ecs update-service \
  --cluster evidentia-production \
  --service evidentia-production-api \
  --desired-count 4

# Scale Worker service during batch verification workloads
aws ecs update-service \
  --cluster evidentia-production \
  --service evidentia-production-worker \
  --desired-count 4
```

### 4.2 Restarting Services Gracefully
ECS Fargate services support rolling zero-downtime restarts:
```bash
aws ecs update-service \
  --cluster evidentia-production \
  --service evidentia-production-api \
  --force-new-deployment
```

### 4.3 MinIO Volume Expansion
To expand the MinIO EBS volume without downtime:
1. Modify EBS volume in AWS:
```bash
VOLUME_ID=$(aws ec2 describe-instances --instance-ids <MINIO_INSTANCE_ID> \
  --query "Reservations[0].Instances[0].BlockDeviceMappings[?DeviceName=='/dev/xvdf'].Ebs.VolumeId" \
  --output text)

aws ec2 modify-volume --volume-id "$VOLUME_ID" --size 300
```
2. Grow filesystem on the instance via SSM:
```bash
aws ssm send-command \
  --instance-ids <MINIO_INSTANCE_ID> \
  --document-name "AWS-RunShellScript" \
  --parameters 'commands=["xfs_growfs -d /data"]'
```

---

## 5. Secrets Rotation Runbook

When rotating credentials (e.g. database password or JWT signing secret):
1. Update the secret in AWS Secrets Manager:
```bash
aws secretsmanager put-secret-value \
  --secret-id "evidentia/prod/db-password" \
  --secret-string "NewGeneratedStrongPassword123!"
```
2. Trigger rolling restart of ECS services to pick up the updated secret value from Secrets Manager:
```bash
aws ecs update-service --cluster evidentia-production --service evidentia-production-api --force-new-deployment
aws ecs update-service --cluster evidentia-production --service evidentia-production-worker --force-new-deployment
```
