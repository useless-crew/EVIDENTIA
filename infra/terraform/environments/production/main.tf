# Evidentia — Production Infrastructure Definition
#
# Orchestrates all modules for a highly available, secure, and resilient
# production deployment on AWS.
# Enforces Zero-Trust network segmentation, KMS encryption at rest,
# in-transit TLS, and least-privilege IAM policies.

# ---------------------------------------------------------------------------
# Networking (VPC, Subnets, NAT Gateways, Security Groups)
# ---------------------------------------------------------------------------

module "networking" {
  source = "../../modules/networking"

  project            = var.project
  environment        = var.environment
  aws_region         = var.aws_region
  vpc_cidr           = var.vpc_cidr
  availability_zones = var.availability_zones
}

# ---------------------------------------------------------------------------
# Container Registry (ECR)
# ---------------------------------------------------------------------------

module "ecr" {
  source = "../../modules/ecr"

  project     = var.project
  environment = var.environment
}

# ---------------------------------------------------------------------------
# IAM Roles & Least-Privilege Policies
# ---------------------------------------------------------------------------

module "iam" {
  source = "../../modules/iam"

  project        = var.project
  environment    = var.environment
  aws_region     = var.aws_region
  aws_account_id = var.aws_account_id
  ecr_api_arn    = module.ecr.api_repository_arn
  ecr_worker_arn = module.ecr.worker_repository_arn
  secrets_arns   = compact([
    var.jwt_secret_arn,
    var.db_app_password_arn,
    var.redis_auth_token_arn,
    var.minio_secret_key_arn,
    var.certificate_signing_key_arn
  ])
}

# ---------------------------------------------------------------------------
# Relational Database (Amazon RDS PostgreSQL 15 - Multi-AZ)
# ---------------------------------------------------------------------------

module "rds" {
  source = "../../modules/rds"

  project                 = var.project
  environment             = var.environment
  subnet_ids              = module.networking.data_subnet_ids
  security_group_id       = module.networking.rds_security_group_id
  instance_class          = var.rds_instance_class
  allocated_storage       = var.rds_allocated_storage
  db_name                 = "evidentia"
  db_username             = "evidentia"
  db_password             = var.db_password
  multi_az                = true
  backup_retention_period = 30
}

# ---------------------------------------------------------------------------
# Distributed Cache & Queue (AWS ElastiCache Redis 7 - TLS Enabled)
# ---------------------------------------------------------------------------

module "redis" {
  source = "../../modules/redis"

  project           = var.project
  environment       = var.environment
  subnet_ids        = module.networking.data_subnet_ids
  security_group_id = module.networking.redis_security_group_id
  node_type         = var.redis_node_type
  auth_token        = var.redis_auth_token
}

# ---------------------------------------------------------------------------
# Object Storage (Private EC2 MinIO with KMS Encrypted EBS)
# ---------------------------------------------------------------------------

module "minio_ec2" {
  source = "../../modules/minio_ec2"

  project             = var.project
  environment         = var.environment
  aws_region          = var.aws_region
  subnet_id           = module.networking.data_subnet_ids[0]
  security_group_id   = module.networking.minio_security_group_id
  instance_type       = "t3.medium"
  ebs_volume_size     = 200
  minio_root_user     = "evidentia_minio"
  minio_root_password = var.minio_root_password
  minio_bucket        = "evidentia-documents"
}

# ---------------------------------------------------------------------------
# Blockchain Provenance Layer (Private EC2 Hyperledger Fabric Node)
# ---------------------------------------------------------------------------

module "fabric_ec2" {
  source = "../../modules/fabric_ec2"

  project           = var.project
  environment       = var.environment
  aws_region        = var.aws_region
  subnet_id         = module.networking.data_subnet_ids[1]
  security_group_id = module.networking.fabric_security_group_id
  instance_type     = "t3.large"
  ebs_volume_size   = 150
}

# ---------------------------------------------------------------------------
# Compute & Load Balancing (ECS Fargate API, Worker & ALB)
# ---------------------------------------------------------------------------

module "ecs" {
  source = "../../modules/ecs"

  project               = var.project
  environment           = var.environment
  aws_region            = var.aws_region
  vpc_id                = module.networking.vpc_id
  public_subnet_ids     = module.networking.public_subnet_ids
  private_subnet_ids    = module.networking.private_subnet_ids
  alb_security_group_id = module.networking.alb_security_group_id
  ecs_security_group_id = module.networking.ecs_security_group_id
  execution_role_arn    = module.iam.ecs_execution_role_arn
  api_task_role_arn     = module.iam.ecs_task_api_role_arn
  worker_task_role_arn  = module.iam.ecs_task_worker_role_arn
  api_image             = var.api_image
  worker_image          = var.worker_image
  api_cpu               = 1024
  api_memory            = 2048
  worker_cpu            = 1024
  worker_memory         = 2048
  api_desired_count     = var.api_desired_count
  acm_certificate_arn   = var.alb_acm_certificate_arn

  environment_variables = {
    APP_ENV              = "production"
    APP_NAME             = "evidentia-backend"
    APP_VERSION          = "1.0.0"
    SERVER_HOST          = "0.0.0.0"
    SERVER_PORT          = "8080"
    TRUSTED_PROXIES      = var.vpc_cidr
    CORS_ALLOWED_ORIGINS = "https://${var.frontend_subdomain}.${var.domain_name}"
    DATABASE_HOST        = module.rds.address
    DATABASE_PORT        = tostring(module.rds.port)
    DATABASE_USER        = "evidentia_app"
    DATABASE_NAME        = module.rds.db_name
    DATABASE_SSLMODE     = "require"
    DATABASE_MAX_OPEN_CONNS = "50"
    DATABASE_MAX_IDLE_CONNS = "10"
    REDIS_ADDR           = module.redis.redis_addr
    REDIS_TLS            = "true"
    MINIO_ENDPOINT       = module.minio_ec2.endpoint
    MINIO_ACCESS_KEY     = "evidentia_minio"
    MINIO_BUCKET         = module.minio_ec2.bucket_name
    MINIO_USE_SSL        = "false"
    MAX_UPLOAD_SIZE      = "52428800"
    FABRIC_ENABLED       = "true"
    FABRIC_PEER_ENDPOINT = module.fabric_ec2.peer_endpoint
    FABRIC_GATEWAY_SSL_HOST_OVERRIDE = "peer0.police.evidentia.local"
    FABRIC_CHANNEL       = "evidentia-channel"
    FABRIC_CHAINCODE     = "evidentia"
    FABRIC_MSP_ID        = "PoliceMSP"
    JWT_ISSUER           = "evidentia-api"
    JWT_AUDIENCE         = "evidentia-client"
    BCRYPT_COST          = "12"
    LOG_LEVEL            = "info"
    LOG_FORMAT           = "json"
  }

  secrets = {
    DATABASE_PASSWORD = var.db_app_password_arn
    REDIS_PASSWORD    = var.redis_auth_token_arn
    MINIO_SECRET_KEY  = var.minio_secret_key_arn
    JWT_SIGNING_KEY   = var.jwt_secret_arn
  }
}

# ---------------------------------------------------------------------------
# Frontend Delivery (S3 Bucket + CloudFront CDN with OAC)
# ---------------------------------------------------------------------------

module "storage" {
  source = "../../modules/storage"

  project             = var.project
  environment         = var.environment
  domain_name         = "${var.frontend_subdomain}.${var.domain_name}"
  acm_certificate_arn = var.cloudfront_acm_certificate_arn
  alb_dns_name        = module.ecs.alb_dns_name
}

# ---------------------------------------------------------------------------
# CloudWatch Monitoring & Alarms
# ---------------------------------------------------------------------------

module "monitoring" {
  source = "../../modules/monitoring"

  project             = var.project
  environment         = var.environment
  ecs_cluster_name    = module.ecs.cluster_name
  api_service_name    = module.ecs.api_service_name
  worker_service_name = module.ecs.worker_service_name
}
