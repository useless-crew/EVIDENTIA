# AWS Deployment Guide

## Prerequisites

1. **AWS Account** with appropriate permissions
2. **AWS CLI** v2 installed and configured
3. **Terraform** >= 1.5 installed
4. **Docker** installed (for building images locally)
5. **GitHub repository** with Actions enabled
6. **(Optional)** A registered domain and its Route 53 hosted zone

## Step 1: Bootstrap Secrets

Create secrets in AWS Secrets Manager before Terraform runs. These
will be injected into ECS tasks as environment variables.

```bash
# Set your region
export AWS_REGION=ap-south-1

# JWT signing key (at least 32 chars, cryptographically random)
aws secretsmanager create-secret \
  --name evidentia/jwt-signing-key \
  --secret-string "$(openssl rand -base64 48)"

# Database passwords
aws secretsmanager create-secret \
  --name evidentia/db-password \
  --secret-string "$(openssl rand -base64 32)"

aws secretsmanager create-secret \
  --name evidentia/db-app-password \
  --secret-string "$(openssl rand -base64 32)"

# MinIO credentials
aws secretsmanager create-secret \
  --name evidentia/minio-access-key \
  --secret-string "$(openssl rand -hex 16)"

aws secretsmanager create-secret \
  --name evidentia/minio-secret-key \
  --secret-string "$(openssl rand -base64 32)"

# Redis AUTH token (16-128 alphanumeric)
aws secretsmanager create-secret \
  --name evidentia/redis-auth-token \
  --secret-string "$(openssl rand -hex 32)"

# Bootstrap admin (optional, one-time)
aws secretsmanager create-secret \
  --name evidentia/bootstrap-admin \
  --secret-string '{"email":"admin@evidentia.example.com","password":"STRONG_PASSWORD","name":"System Admin"}'
```

## Step 2: Provision Infrastructure

```bash
cd infra/terraform/environments/demo

# Copy and edit variables
cp terraform.tfvars.example terraform.tfvars
# Edit terraform.tfvars with your account ID, region, secret ARNs, etc.

# Initialize Terraform
terraform init

# Preview changes
terraform plan

# Apply (review the plan first!)
terraform apply
```

Note the outputs — you'll need:
- `ecr_api_url` / `ecr_worker_url` — for pushing Docker images
- `frontend_bucket` — for deploying the Angular build
- `cloudfront_distribution_id` — for cache invalidation
- `rds_endpoint` — for running migrations

## Step 3: Build and Push Docker Images

```bash
# Get ECR login
aws ecr get-login-password --region $AWS_REGION | \
  docker login --username AWS --password-stdin \
  $AWS_ACCOUNT_ID.dkr.ecr.$AWS_REGION.amazonaws.com

# Build the backend image
docker build -t evidentia-api ./backend

# Tag with commit SHA
IMAGE_TAG="sha-$(git rev-parse --short HEAD)"
docker tag evidentia-api $ECR_API_URL:$IMAGE_TAG
docker tag evidentia-api $ECR_WORKER_URL:$IMAGE_TAG

# Push
docker push $ECR_API_URL:$IMAGE_TAG
docker push $ECR_WORKER_URL:$IMAGE_TAG
```

## Step 4: Run Database Migrations

Run migrations as a one-shot ECS task with the privileged schema-owner
credentials (never the runtime `evidentia_app` role):

```bash
aws ecs run-task \
  --cluster evidentia-demo \
  --task-definition evidentia-demo-api \
  --launch-type FARGATE \
  --network-configuration '{
    "awsvpcConfiguration": {
      "subnets": ["subnet-xxx"],
      "securityGroups": ["sg-xxx"],
      "assignPublicIp": "DISABLED"
    }
  }' \
  --overrides '{
    "containerOverrides": [{
      "name": "api",
      "command": ["/app/migrate", "up"],
      "environment": [
        {"name": "DATABASE_USER", "value": "evidentia"},
        {"name": "DATABASE_PASSWORD", "value": "MASTER_PASSWORD_FROM_SECRETS"}
      ]
    }]
  }'
```

## Step 5: Deploy Frontend

```bash
# Build Angular production bundle
cd frontend
npm ci
npm run build

# Sync to S3
aws s3 sync dist/evidentia/browser/ s3://$FRONTEND_BUCKET/ --delete \
  --cache-control "public, max-age=31536000, immutable" \
  --exclude "index.html"

# Upload index.html with no-cache (SPA routing)
aws s3 cp dist/evidentia/browser/index.html \
  s3://$FRONTEND_BUCKET/index.html \
  --cache-control "no-cache"

# Invalidate CloudFront
aws cloudfront create-invalidation \
  --distribution-id $CLOUDFRONT_DIST_ID \
  --paths "/index.html"
```

## Step 6: Configure GitHub Actions

Set these repository secrets for automated deployments:

| Secret | Value |
|--------|-------|
| `AWS_ACCOUNT_ID` | Your AWS account ID |
| `AWS_REGION` | `ap-south-1` |
| `AWS_ROLE_ARN` | IAM role ARN for OIDC |
| `ECS_CLUSTER` | `evidentia-demo` |
| `ECS_API_SERVICE` | `evidentia-api` |
| `ECS_WORKER_SERVICE` | `evidentia-worker` |
| `FRONTEND_S3_BUCKET` | S3 bucket name |
| `CLOUDFRONT_DIST_ID` | CloudFront distribution ID |
| `APP_DOMAIN` | Your domain or CloudFront domain |

## Step 7: Verify Deployment

```bash
# Run the smoke test
export API_BASE_URL=https://your-domain.example.com
export ADMIN_EMAIL=admin@evidentia.example.com
export ADMIN_PASS=your-bootstrap-password
./scripts/smoke-test.sh
```

## Environment Variables Reference

All existing environment variables from `backend/.env.example` work
unchanged. AWS-specific additions:

| Variable | AWS Value | Notes |
|----------|-----------|-------|
| `APP_ENV` | `production` | Enables Gin release mode, strict CORS |
| `DATABASE_HOST` | RDS endpoint | From Terraform output |
| `DATABASE_SSLMODE` | `require` | RDS always supports TLS |
| `REDIS_ADDR` | ElastiCache endpoint:6379 | From Terraform output |
| `REDIS_TLS` | `true` | ElastiCache in-transit encryption |
| `REDIS_PASSWORD` | AUTH token | From Secrets Manager |
| `MINIO_ENDPOINT` | EC2 private IP:9000 | Private subnet only |
| `MINIO_USE_SSL` | `true` | If MinIO is configured with TLS |
| `TRUSTED_PROXIES` | VPC CIDR or ALB IPs | For `X-Forwarded-For` trust |
| `CORS_ALLOWED_ORIGINS` | `https://your-domain.com` | Never `*` in production |
| `DISABLE_EMBEDDED_WORKER` | `true` | API task only; worker is separate |

## Operational Procedures

### Viewing Logs

```bash
# API logs
aws logs tail /ecs/evidentia-demo/api --follow

# Worker logs
aws logs tail /ecs/evidentia-demo/worker --follow

# Search for errors
aws logs filter-log-events \
  --log-group-name /ecs/evidentia-demo/api \
  --filter-pattern "ERROR"
```

### Scaling

```bash
# Scale API to 3 replicas
aws ecs update-service \
  --cluster evidentia-demo \
  --service evidentia-api \
  --desired-count 3
```

### Database Backup

```bash
# Manual RDS snapshot
aws rds create-db-snapshot \
  --db-instance-identifier evidentia-demo-postgres \
  --db-snapshot-identifier "manual-$(date +%Y%m%d-%H%M%S)"
```

### Rollback

```bash
# Update ECS to use a previous image tag
aws ecs update-service \
  --cluster evidentia-demo \
  --service evidentia-api \
  --task-definition evidentia-demo-api:PREVIOUS_REVISION
```

## Cost Estimate (SIH Demo)

See [aws-cost-estimate.md](aws-cost-estimate.md) for a detailed breakdown.
**Estimated monthly cost for demo: ~$80-120 USD.**
