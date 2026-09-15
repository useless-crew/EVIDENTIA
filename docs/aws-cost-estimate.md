# AWS Cost Estimate

## SIH Demo Environment

Monthly cost estimate for the demo deployment in `ap-south-1` (Mumbai).
All prices are approximate and based on current AWS pricing.

| Service | Size | Monthly Cost (USD) |
|---------|------|--------------------|
| **ECS Fargate — API** | 0.5 vCPU, 1 GB, 24/7 | ~$15 |
| **ECS Fargate — Worker** | 0.25 vCPU, 0.5 GB, 24/7 | ~$8 |
| **RDS PostgreSQL** | db.t3.micro, 20 GB gp3 | ~$15 |
| **ElastiCache Redis** | cache.t3.micro | ~$12 |
| **EC2 — MinIO** | t3.small, 50 GB EBS | ~$17 |
| **ALB** | Hourly + LCU | ~$18 |
| **NAT Gateway** | Hourly + data | ~$33 |
| **CloudFront** | Free tier (1 TB) | ~$0 |
| **S3** | < 1 GB frontend | ~$0.03 |
| **ECR** | < 5 GB images | ~$0.50 |
| **CloudWatch** | Logs + alarms | ~$3 |
| **Secrets Manager** | 6 secrets | ~$2.40 |
| **Total** | | **~$124** |

### Cost Optimization Options

1. **Remove NAT Gateway** (saves ~$33/month): Use VPC endpoints for
   ECR, S3, CloudWatch, and Secrets Manager instead. ECS tasks don't
   need general Internet access.

2. **Schedule non-production hours** (saves ~40%): Scale ECS to 0 and
   stop the EC2 instance outside demo hours using EventBridge + Lambda.

3. **Use Fargate Spot for Worker** (saves ~70% on worker): The worker
   processes async jobs; interruptions cause retries, not data loss.

4. **Use RDS `db.t3.micro` free tier**: First 12 months, the RDS
   instance is included in the AWS free tier.

### With Optimizations (Estimated)

| Configuration | Monthly Cost |
|--------------|-------------|
| Full 24/7 demo | ~$124 |
| NAT → VPC endpoints | ~$91 |
| + Fargate Spot worker | ~$85 |
| + Scheduled (12h/day) | ~$52 |
| + RDS free tier | ~$37 |

## Production Scaling Estimate

For a production deployment handling ~100 concurrent users:

| Service | Size | Monthly Cost (USD) |
|---------|------|--------------------|
| ECS API (2 tasks) | 1 vCPU, 2 GB each | ~$120 |
| ECS Worker | 0.5 vCPU, 1 GB | ~$30 |
| RDS PostgreSQL | db.t3.medium, Multi-AZ, 100 GB | ~$140 |
| ElastiCache Redis | cache.t3.small, Multi-AZ | ~$50 |
| EC2 MinIO (HA pair) | t3.medium x2, 500 GB EBS | ~$100 |
| EC2 Fabric | t3.medium, 50 GB | ~$35 |
| ALB | Hourly + LCU | ~$25 |
| NAT Gateway | Hourly + data | ~$35 |
| CloudFront | 1-10 TB | ~$50 |
| **Total** | | **~$585** |
