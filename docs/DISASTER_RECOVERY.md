# Evidentia — Disaster Recovery, Backup & Business Continuity Plan

**Target RPO (Recovery Point Objective):** < 5 Minutes  
**Target RTO (Recovery Time Objective):** < 60 Minutes  
**Data Classification:** Critical Forensic Evidence & Judicial Records  

---

## 1. Backup Schedule & Retention Matrix

| Tier / Component | Backup Mechanism | Frequency | Retention Period | Storage Location | Encryption |
|---|---|---|---|---|---|
| **Amazon RDS PostgreSQL** | Automated Continuous Backups + Daily Snapshots | Continuous WAL + Daily Snapshot (03:00 UTC) | 30 Days | Cross-AZ S3 (Managed by RDS) | KMS (`aws/rds`) |
| **MinIO Evidence EBS** | Amazon Data Lifecycle Manager (DLM) EBS Snapshots | Every 6 Hours | 90 Days | Amazon EBS Snapshot Repository | KMS (Custom CMK) |
| **Hyperledger Fabric EBS**| DLM EBS Snapshots | Daily (02:00 UTC) | 30 Days | Amazon EBS Snapshot Repository | KMS (Custom CMK) |
| **Terraform State** | S3 Bucket Versioning + Object Lock | On Every State Change | Indefinite | Dedicated State S3 Bucket | KMS (`aws/s3`) |
| **Application Secrets** | AWS Secrets Manager Versioning | On Every Secret Rotation | Previous Versions Stored | Secrets Manager Store | KMS (`aws/secretsmanager`) |

---

## 2. Disaster Recovery Scenarios & Playbooks

### Scenario A: Single Availability Zone Outage
- **Impact:** Failure of one data center within the AWS region (`ap-south-1a`).
- **Automated Mitigation:**
  - **RDS:** Automatically fails over to the synchronous standby instance in `ap-south-1b` within 60–120 seconds. DNS endpoint remains unchanged.
  - **ALB & ECS Fargate:** Health checks detect failed tasks in the affected AZ and automatically redirect traffic to healthy tasks in other AZs.
- **RTO:** < 2 minutes  
- **RPO:** 0 minutes (Synchronous replication)

---

### Scenario B: Database Corruption or Accidental Modification
- **Objective:** Restore PostgreSQL to a clean point-in-time immediately preceding the incident.
- **Procedure:**
1. Determine the exact timestamp (UTC) of the corruption incident.
2. Launch a point-in-time restore to a new RDS DB instance:
```bash
RESTORE_TIME="2026-09-19T06:45:00Z"

aws rds restore-db-instance-to-point-in-time \
  --source-db-instance-identifier evidentia-production-postgres \
  --target-db-instance-identifier evidentia-production-postgres-restored \
  --restore-time "$RESTORE_TIME" \
  --db-subnet-group-name evidentia-production-db-subnet \
  --vpc-security-group-ids <RDS_SECURITY_GROUP_ID> \
  --multi-az
```
3. Wait for the restored instance to reach `available` status:
```bash
aws rds wait db-instance-available --db-instance-identifier evidentia-production-postgres-restored
```
4. Verify data integrity, run audit chain verification:
```bash
# Execute read-only verification query against restored endpoint
```
5. Update DNS or Terraform state to point to the restored DB endpoint.

---

### Scenario C: MinIO Object Store Recovery from EBS Snapshot
- **Objective:** Restore the entire evidence repository if the MinIO EC2 instance or EBS volume fails.
- **Procedure:**
1. Locate the latest healthy EBS snapshot for the MinIO evidence volume:
```bash
SNAPSHOT_ID=$(aws ec2 describe-snapshots \
  --filters "Name=tag:Component,Values=storage" "Name=status,Values=completed" \
  --query "reverse(sort_by(Snapshots, &StartTime))[0].SnapshotId" \
  --output text)

echo "Latest snapshot: $SNAPSHOT_ID"
```
2. Create a new gp3 EBS volume from the snapshot in the target AZ:
```bash
TARGET_AZ="ap-south-1a"
NEW_VOLUME_ID=$(aws ec2 create-volume \
  --snapshot-id "$SNAPSHOT_ID" \
  --availability-zone "$TARGET_AZ" \
  --volume-type gp3 \
  --encrypted \
  --query "VolumeId" --output text)

aws ec2 wait volume-available --volume-ids "$NEW_VOLUME_ID"
```
3. Detach corrupted volume (if still attached) and attach the restored volume to the MinIO instance:
```bash
aws ec2 detach-volume --volume-id <CORRUPTED_VOLUME_ID> || true
aws ec2 attach-volume --volume-id "$NEW_VOLUME_ID" --instance-id <MINIO_INSTANCE_ID> --device /dev/xvdf
```
4. Restart MinIO service via SSM:
```bash
aws ssm send-command \
  --instance-ids <MINIO_INSTANCE_ID> \
  --document-name "AWS-RunShellScript" \
  --parameters 'commands=["mount /dev/xvdf /data || true", "systemctl restart minio"]'
```
5. Run hash integrity verification against a sample set of documents.

---

### Scenario D: Hyperledger Fabric Ledger State Recovery
- **Objective:** Restore the permissioned ledger node in the event of filesystem or state corruption.
- **Procedure:**
1. If the peer ledger state is corrupted but other network peers exist:
   - Re-provision the peer container with a clean state.
   - Re-join the peer to `evidentia-channel`.
   - The peer will automatically replay and sync blocks from the orderer and remaining peers.
2. If the single-node demo ledger suffered volume failure:
   - Restore the `/var/hyperledger` EBS volume from the latest daily snapshot.
   - Attach volume and restart Docker / Fabric containers:
```bash
aws ssm send-command \
  --instance-ids <FABRIC_INSTANCE_ID> \
  --document-name "AWS-RunShellScript" \
  --parameters 'commands=["cd /opt/evidentia-fabric && docker compose -f docker-compose-fabric.yml up -d"]'
```
3. Validate chaincode query status:
```bash
# Verify ledger height and anchor consistency
```

---

### Scenario E: Complete Regional Disaster (Multi-Region Recovery)
- **Objective:** Re-create the entire platform in a secondary AWS region (e.g. `ap-southeast-1` Singapore) from code and cross-region backups.
- **Procedure:**
1. Clone repository and configure AWS credentials for the target recovery region.
2. Copy the latest RDS snapshot and EBS snapshots to the recovery region.
3. Configure `infra/terraform/environments/production/terraform.tfvars` for the recovery region:
   - Update `aws_region = "ap-southeast-1"`
   - Update ACM certificate ARNs
4. Execute Terraform:
```bash
terraform init
terraform apply -auto-approve
```
5. Restore database from cross-region RDS snapshot.
6. Mount restored MinIO snapshot volume to the new EC2 instance.
7. Deploy frontend to the new S3 bucket and update Route 53 failover DNS records to route traffic to the secondary region.
8. Execute post-recovery smoke test (`./scripts/smoke-test.sh`).

---

## 3. Post-Restoration Evidence Integrity Verification

Following any disaster recovery restoration:
1. Run cryptographic audit chain verification:
```bash
curl -X POST https://api.evidentia.example.com/api/v1/audit/verify-chain \
  -H "Authorization: Bearer $ADMIN_TOKEN"
```
2. Assert that verification status returns `VERIFIED` and zero tamper alerts are triggered.
3. Verify sample document downloads:
```bash
curl -X GET https://api.evidentia.example.com/api/v1/documents/<SAMPLE_ID>/download \
  -H "Authorization: Bearer $ADMIN_TOKEN" -o /tmp/sample_download
sha256sum /tmp/sample_download
```
Compare with the original hash stored in PostgreSQL metadata.
