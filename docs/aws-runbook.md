# AWS Runbook

## Deployment Sequence

Execute these steps in order for a fresh deployment:

### Phase 1: Infrastructure (Terraform)

1. Create AWS Secrets Manager secrets (see aws-deployment.md Step 1)
2. `cd infra/terraform/environments/demo`
3. `cp terraform.tfvars.example terraform.tfvars` — edit with real values
4. `terraform init`
5. `terraform plan` — review carefully
6. `terraform apply` — note all outputs

### Phase 2: Container Images

7. `aws ecr get-login-password | docker login --username AWS --password-stdin $ECR_URL`
8. `docker build -t evidentia-api ./backend`
9. `docker tag evidentia-api $ECR_API_URL:sha-$(git rev-parse --short HEAD)`
10. `docker push $ECR_API_URL:sha-...`
11. Same for worker (same image, different ECR repo)

### Phase 3: MinIO Setup (EC2)

12. SSH to MinIO EC2 instance
13. Install MinIO server binary
14. Create systemd service (or Docker)
15. Configure with access key and secret key from Secrets Manager
16. Create the `evidentia` bucket: `mc mb local/evidentia`
17. Verify: `mc ls local/evidentia`

### Phase 4: Database Setup

18. Run migrations via ECS run-task with schema-owner credentials
19. Verify: connect to RDS and check tables exist
20. Create the `evidentia_app` runtime role with limited privileges
21. Verify RLS policies are active

### Phase 5: Application Deployment

22. Update ECS API task definition with final environment variables
23. Update ECS Worker task definition similarly
24. Deploy API service: `aws ecs update-service --force-new-deployment`
25. Deploy Worker service similarly
26. Wait for stability: `aws ecs wait services-stable`
27. Verify: `curl https://$DOMAIN/health` → `{"status":"ok"}`
28. Verify: `curl https://$DOMAIN/ready` → all deps `"ok"`

### Phase 6: Frontend Deployment

29. `cd frontend && npm ci && npm run build`
30. `aws s3 sync dist/evidentia/browser/ s3://$BUCKET/ --delete`
31. `aws cloudfront create-invalidation --paths "/index.html"`
32. Verify: open `https://$DOMAIN` in browser

### Phase 7: Post-Deployment

33. Run smoke test: `./scripts/smoke-test.sh`
34. Verify CloudWatch logs are flowing
35. Verify CloudWatch alarms are in OK state
36. Create initial admin via bootstrap (if configured)
37. Test login flow end-to-end
38. Test evidence upload + verification
39. Test audit trail
40. Verify security headers (use browser dev tools)
41. Verify CORS (cross-origin request from CloudFront to ALB)
42. Document deployment in team wiki

---

## Rollback Procedures

### Application Rollback

```bash
# Find previous task definition revision
aws ecs list-task-definitions --family evidentia-demo-api --sort DESC --max-items 5

# Roll back to previous revision
aws ecs update-service \
  --cluster evidentia-demo \
  --service evidentia-api \
  --task-definition evidentia-demo-api:PREVIOUS_REVISION

# Wait for stability
aws ecs wait services-stable --cluster evidentia-demo --services evidentia-api
```

### Database Rollback

```bash
# Run down migration (DESTRUCTIVE — review the migration SQL first)
aws ecs run-task \
  --cluster evidentia-demo \
  --task-definition evidentia-demo-api \
  --overrides '{"containerOverrides":[{"name":"api","command":["/app/migrate","down","1"]}]}'

# Or restore from RDS snapshot
aws rds restore-db-instance-from-db-snapshot \
  --db-instance-identifier evidentia-demo-postgres-restored \
  --db-snapshot-identifier SNAPSHOT_ID
```

### Frontend Rollback

```bash
# S3 versioning is enabled — restore previous version
aws s3api list-object-versions --bucket $BUCKET --prefix index.html --max-items 5

# Copy the previous version over current
aws s3api copy-object \
  --bucket $BUCKET \
  --copy-source "$BUCKET/index.html?versionId=PREVIOUS_VERSION" \
  --key index.html

aws cloudfront create-invalidation --distribution-id $DIST_ID --paths "/index.html"
```

---

## Disaster Recovery

### RDS

- **RPO**: Point-in-time recovery within backup retention (7 days)
- **RTO**: ~15 minutes for point-in-time restore
- **Procedure**: `aws rds restore-db-instance-to-point-in-time`

### MinIO (EC2)

- **RPO**: Depends on EBS snapshot frequency
- **Procedure**: Create AMI of MinIO instance, restore from snapshot
- **Recommendation**: Schedule daily EBS snapshots via AWS Backup

### ElastiCache

- **RPO**: Daily snapshot window
- **Procedure**: Create new cluster from snapshot
- **Note**: Redis data is ephemeral (rate limit counters, Asynq queues);
  loss is tolerable — the application recovers gracefully

### Full Recovery

1. Restore RDS from snapshot/PITR
2. Restore MinIO EC2 from AMI/EBS snapshot
3. ElastiCache — create new; rate limit counters reset naturally
4. ECS services — redeploy from ECR images (immutable tags)
5. Frontend — redeploy from Git (build + S3 sync)
6. Run smoke test

---

## Monitoring Checklist

### Daily

- [ ] Check CloudWatch alarms dashboard — all green
- [ ] Review ECS service events for restarts
- [ ] Spot-check API latency in CloudWatch metrics

### Weekly

- [ ] Review CloudWatch Logs Insights for error patterns
- [ ] Check RDS storage utilization trend
- [ ] Verify RDS automated backups are running
- [ ] Review ECR image scan results

### Monthly

- [ ] Rotate secrets in Secrets Manager
- [ ] Review and update security group rules
- [ ] Check for available RDS/ElastiCache engine upgrades
- [ ] Review cost allocation tags and actual spend

---

## Troubleshooting

### ECS Task Keeps Restarting

1. Check CloudWatch logs: `aws logs tail /ecs/evidentia-demo/api --follow`
2. Look for configuration validation errors (fail-closed on startup)
3. Verify Secrets Manager ARNs are correct in task definition
4. Verify security groups allow ECS → RDS/Redis/MinIO traffic
5. Check RDS/ElastiCache are in the same VPC and subnet group

### ALB Returns 502/503

1. Check ECS service event log: `aws ecs describe-services`
2. Verify target group health: `aws elbv2 describe-target-health`
3. Check if `/health` is responding on the container
4. Verify security group allows ALB → ECS on port 8080

### Database Connection Errors

1. Verify `DATABASE_SSLMODE=require` (RDS requires TLS)
2. Check `DATABASE_HOST` matches RDS endpoint (not IP)
3. Verify `evidentia_app` role exists and has correct grants
4. Check RDS security group allows inbound from ECS security group
5. Check RDS connection count vs `DATABASE_MAX_OPEN_CONNS`

### Redis/ElastiCache Connection Errors

1. Verify `REDIS_TLS=true` (ElastiCache in-transit encryption)
2. Verify `REDIS_PASSWORD` matches the AUTH token
3. Check security group allows ECS → ElastiCache on port 6379
4. Verify ElastiCache is in the same VPC subnet group
