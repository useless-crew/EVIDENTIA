# Evidentia — AWS Production Deployment Runbook

This guide covers the end-to-end procedure for deploying the Evidentia platform on AWS.

---

## 1. Prerequisites & CLI Tools

Ensure the following tools are installed and authenticated:
- **AWS CLI** (`aws --version >= 2.15`): Configured with an IAM administrative role.
- **Terraform** (`terraform version >= 1.7.5`): HashiCorp Terraform CLI.
- **Docker** (`docker version >= 24.0` with Buildx): For building container images.
- **Go** (`go version >= 1.22`): For running local validation tests.
- **Node.js** (`node >= 20.x`, `npm >= 10.x`): For building Angular frontend.

---

## 2. Pre-Deployment Setup

### 2.1 Route 53 & ACM Certificates
Two SSL/TLS certificates are required:
1. **Regional Certificate (`ap-south-1`):** For the Application Load Balancer.
   - Domain names: `api.evidentia.example.com`, `evidentia.example.com`
2. **Global Certificate (`us-east-1`):** For CloudFront CDN.
   - Domain names: `app.evidentia.example.com`, `evidentia.example.com`

```bash
# Request Regional Certificate in ap-south-1
aws acm request-certificate \
  --region ap-south-1 \
  --domain-name "api.evidentia.example.com" \
  --validation-method DNS

# Request CloudFront Certificate in us-east-1
aws acm request-certificate \
  --region us-east-1 \
  --domain-name "app.evidentia.example.com" \
  --validation-method DNS
```

Complete DNS validation in Route 53 before continuing.

---

### 2.2 Provision Secrets in AWS Secrets Manager
Generate cryptographically strong random secrets and store them in AWS Secrets Manager:

```bash
# 1. JWT Signing Key (minimum 32 random characters)
JWT_KEY=$(openssl rand -base64 48)
aws secretsmanager create-secret \
  --region ap-south-1 \
  --name "evidentia/prod/jwt-secret" \
  --secret-string "$JWT_KEY"

# 2. Database Passwords
DB_MASTER_PASS=$(openssl rand -base64 24)
DB_APP_PASS=$(openssl rand -base64 24)
aws secretsmanager create-secret \
  --region ap-south-1 \
  --name "evidentia/prod/db-password" \
  --secret-string "$DB_APP_PASS"

# 3. Redis Auth Token
REDIS_TOKEN=$(openssl rand -base64 32)
aws secretsmanager create-secret \
  --region ap-south-1 \
  --name "evidentia/prod/redis-auth" \
  --secret-string "$REDIS_TOKEN"

# 4. MinIO Secret Key
MINIO_SECRET=$(openssl rand -base64 24)
aws secretsmanager create-secret \
  --region ap-south-1 \
  --name "evidentia/prod/minio-secret" \
  --secret-string "$MINIO_SECRET"
```

Save the generated ARNs for use in your `terraform.tfvars`.

---

## 3. Container Images & ECR Deployment

### 3.1 Authenticate Docker to ECR
```bash
AWS_ACCOUNT_ID=$(aws sts get-caller-identity --query Account --output text)
AWS_REGION="ap-south-1"

aws ecr get-login-password --region $AWS_REGION | docker login \
  --username AWS \
  --password-stdin "${AWS_ACCOUNT_ID}.dkr.ecr.${AWS_REGION}.amazonaws.com"
```

### 3.2 Build and Push Images with Git SHA Tags
```bash
GIT_SHA=$(git rev-parse --short HEAD)
API_IMAGE="${AWS_ACCOUNT_ID}.dkr.ecr.${AWS_REGION}.amazonaws.com/evidentia-api:sha-${GIT_SHA}"
WORKER_IMAGE="${AWS_ACCOUNT_ID}.dkr.ecr.${AWS_REGION}.amazonaws.com/evidentia-worker:sha-${GIT_SHA}"

# Build and push API image
docker build -f backend/Dockerfile.api -t "$API_IMAGE" ./backend
docker push "$API_IMAGE"

# Build and push Worker image
docker build -f backend/Dockerfile.worker -t "$WORKER_IMAGE" ./backend
docker push "$WORKER_IMAGE"
```

---

## 4. Terraform Infrastructure Provisioning

> [!IMPORTANT]
> **Human Approval Checkpoint (Phase 29):**
> Run `terraform plan` first and review all planned additions before executing `terraform apply`.

```bash
cd infra/terraform/environments/production

# 1. Initialize working directory
terraform init

# 2. Format and validate
terraform fmt -check
terraform validate

# 3. Create execution plan
terraform plan -out=tfplan.binary

# 4. Review plan, then apply
terraform apply tfplan.binary
```

---

## 5. Database Initialization & Migrations

Execute schema migrations and reference data seeding via a one-shot task before launching application services:

```bash
# Obtain RDS endpoint from Terraform output
RDS_HOST=$(terraform output -raw rds_endpoint | cut -d':' -f1)

# Run migrations using the privileged migrator user
cd ../../../backend
DATABASE_MIGRATOR_USER=evidentia \
DATABASE_MIGRATOR_PASSWORD="$DB_MASTER_PASS" \
DATABASE_HOST="$RDS_HOST" \
DATABASE_PORT=5432 \
DATABASE_NAME=evidentia \
DATABASE_SSLMODE=require \
go run ./cmd/migrate up

# Seed initial roles and permissions
DATABASE_MIGRATOR_USER=evidentia \
DATABASE_MIGRATOR_PASSWORD="$DB_MASTER_PASS" \
DATABASE_HOST="$RDS_HOST" \
DATABASE_PORT=5432 \
DATABASE_NAME=evidentia \
DATABASE_SSLMODE=require \
go run ./cmd/migrate seed
```

---

## 6. Frontend Build & CloudFront Deployment

```bash
cd ../frontend

# 1. Install dependencies
npm ci

# 2. Build production bundle (Angular 22)
npm run build

# 3. Obtain S3 bucket name and CloudFront distribution ID from Terraform
cd ../infra/terraform/environments/production
FRONTEND_BUCKET=$(terraform output -raw frontend_s3_bucket)
CLOUDFRONT_ID=$(terraform output -raw cloudfront_distribution_id)

# 4. Sync assets to private S3 bucket
aws s3 sync ../../../frontend/dist/evidentia/browser/ "s3://${FRONTEND_BUCKET}/" \
  --delete \
  --cache-control "public, max-age=31536000, immutable" \
  --exclude "index.html"

# 5. Copy index.html with no-cache headers
aws s3 cp ../../../frontend/dist/evidentia/browser/index.html "s3://${FRONTEND_BUCKET}/index.html" \
  --cache-control "no-cache, no-store, must-revalidate"

# 6. Invalidate CloudFront cache
aws cloudfront create-invalidation \
  --distribution-id "$CLOUDFRONT_ID" \
  --paths "/*"
```

---

## 7. Post-Deployment Verification & Smoke Tests

Execute the automated smoke test script against the newly deployed production API:

```bash
cd ../../../

export API_BASE_URL="https://api.evidentia.example.com"
export ADMIN_EMAIL="admin@evidentia.example.com"
export ADMIN_PASS="YourBootstrapAdminPassword"

./scripts/smoke-test.sh
```

Verify that:
1. `GET /health` returns `200 OK` (`{"status":"ok"}`).
2. `GET /ready` returns `200 OK` with `postgres: ok`, `redis: ok`, `minio: ok`.
3. Authentication, user profile retrieval, and case creation succeed.

---

## 8. Rollback Procedures

### 8.1 Frontend Rollback
To roll back a bad frontend deployment:
1. Re-sync previous S3 release from your artifact repository or CI cache.
2. Invalidate CloudFront: `aws cloudfront create-invalidation --distribution-id $CLOUDFRONT_ID --paths "/*"`.

### 8.2 ECS Fargate Rollback
To roll back the API or Worker services:
```bash
# Update service to point to previous stable task definition revision
aws ecs update-service \
  --cluster evidentia-production \
  --service evidentia-production-api \
  --task-definition evidentia-production-api:<PREVIOUS_REVISION_NUMBER>

aws ecs update-service \
  --cluster evidentia-production \
  --service evidentia-production-worker \
  --task-definition evidentia-production-worker:<PREVIOUS_REVISION_NUMBER>
```

### 8.3 Database Rollback
If a schema migration must be rolled back:
```bash
cd backend
DATABASE_MIGRATOR_USER=evidentia \
DATABASE_MIGRATOR_PASSWORD="$DB_MASTER_PASS" \
DATABASE_HOST="$RDS_HOST" \
DATABASE_PORT=5432 \
DATABASE_NAME=evidentia \
DATABASE_SSLMODE=require \
go run ./cmd/migrate down 1
```
*(Never roll back schema migrations that would destroy valid forensic evidence or tamper-evident audit rows).*
