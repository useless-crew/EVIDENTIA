# Evidentia — Demo Environment
#
# Composes all modules into an affordable SIH demo deployment.
# See docs/aws-deployment.md for the full walkthrough.

terraform {
  required_version = ">= 1.5"
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.0"
    }
  }
}

provider "aws" {
  region = var.aws_region

  default_tags {
    tags = {
      Project     = "evidentia"
      Environment = var.environment
      ManagedBy   = "terraform"
    }
  }
}

# ---------------------------------------------------------------------------
# Variables
# ---------------------------------------------------------------------------

variable "aws_region" {
  description = "AWS region for deployment"
  type        = string
  default     = "ap-south-1"
}

variable "environment" {
  type    = string
  default = "demo"
}

variable "aws_account_id" {
  description = "AWS account ID"
  type        = string
}

variable "availability_zones" {
  description = "AZs to use"
  type        = list(string)
  default     = ["ap-south-1a", "ap-south-1b"]
}

variable "db_password" {
  description = "RDS master password"
  type        = string
  sensitive   = true
}

variable "redis_auth_token" {
  description = "ElastiCache Redis AUTH token"
  type        = string
  sensitive   = true
}

variable "api_image" {
  description = "Full ECR URI for the API image"
  type        = string
}

variable "worker_image" {
  description = "Full ECR URI for the worker image"
  type        = string
}

variable "domain_name" {
  description = "Domain name (e.g. app.evidentia.example.com)"
  type        = string
  default     = ""
}

variable "acm_certificate_arn" {
  description = "ACM certificate ARN for HTTPS"
  type        = string
  default     = ""
}

variable "jwt_secret_arn" {
  description = "Secrets Manager ARN for JWT_SIGNING_KEY"
  type        = string
  default     = ""
}

variable "minio_secret_arn" {
  description = "Secrets Manager ARN for MINIO_SECRET_KEY"
  type        = string
  default     = ""
}

# ---------------------------------------------------------------------------
# Modules
# ---------------------------------------------------------------------------

module "networking" {
  source = "../../modules/networking"

  project            = "evidentia"
  environment        = var.environment
  aws_region         = var.aws_region
  availability_zones = var.availability_zones
}

module "ecr" {
  source = "../../modules/ecr"

  project     = "evidentia"
  environment = var.environment
}

module "iam" {
  source = "../../modules/iam"

  project        = "evidentia"
  environment    = var.environment
  aws_region     = var.aws_region
  aws_account_id = var.aws_account_id
  ecr_api_arn    = module.ecr.api_repository_arn
  ecr_worker_arn = module.ecr.worker_repository_arn
  secrets_arns   = compact([var.jwt_secret_arn, var.minio_secret_arn])
}

module "rds" {
  source = "../../modules/rds"

  project           = "evidentia"
  environment       = var.environment
  subnet_ids        = module.networking.data_subnet_ids
  security_group_id = module.networking.rds_security_group_id
  instance_class    = "db.t3.micro"
  allocated_storage = 20
  db_password       = var.db_password
  multi_az          = false
}

module "redis" {
  source = "../../modules/redis"

  project           = "evidentia"
  environment       = var.environment
  subnet_ids        = module.networking.data_subnet_ids
  security_group_id = module.networking.redis_security_group_id
  node_type         = "cache.t3.micro"
  auth_token        = var.redis_auth_token
}

module "ecs" {
  source = "../../modules/ecs"

  project               = "evidentia"
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
  api_cpu               = 512
  api_memory            = 1024
  worker_cpu            = 256
  worker_memory         = 512
  acm_certificate_arn   = var.acm_certificate_arn

  environment_variables = {
    APP_ENV            = "production"
    APP_VERSION        = "demo"
    DATABASE_HOST      = module.rds.address
    DATABASE_PORT      = tostring(module.rds.port)
    DATABASE_USER      = "evidentia_app"
    DATABASE_NAME      = module.rds.db_name
    DATABASE_SSLMODE   = "require"
    REDIS_ADDR         = module.redis.redis_addr
    REDIS_TLS          = "true"
    MINIO_USE_SSL      = "true"
    LOG_LEVEL          = "info"
    LOG_FORMAT         = "json"
    CORS_ALLOWED_ORIGINS = var.domain_name != "" ? "https://${var.domain_name}" : "*"
    JWT_ISSUER         = "evidentia-api"
    JWT_AUDIENCE       = "evidentia-client"
    BCRYPT_COST        = "12"
    FABRIC_ENABLED     = "false"
  }

  secrets = {
    DATABASE_PASSWORD = var.jwt_secret_arn != "" ? var.jwt_secret_arn : ""
    JWT_SIGNING_KEY   = var.jwt_secret_arn
    REDIS_PASSWORD    = var.minio_secret_arn != "" ? var.minio_secret_arn : ""
    MINIO_SECRET_KEY  = var.minio_secret_arn
  }
}

module "storage" {
  source = "../../modules/storage"

  project             = "evidentia"
  environment         = var.environment
  domain_name         = var.domain_name
  acm_certificate_arn = var.acm_certificate_arn
  alb_dns_name        = module.ecs.alb_dns_name
}

module "monitoring" {
  source = "../../modules/monitoring"

  project             = "evidentia"
  environment         = var.environment
  ecs_cluster_name    = module.ecs.cluster_name
  api_service_name    = module.ecs.api_service_name
  worker_service_name = module.ecs.worker_service_name
}

# ---------------------------------------------------------------------------
# Outputs
# ---------------------------------------------------------------------------

output "alb_dns_name" {
  description = "ALB DNS name — point your domain's CNAME/alias here"
  value       = module.ecs.alb_dns_name
}

output "cloudfront_domain" {
  description = "CloudFront distribution domain"
  value       = module.storage.cloudfront_domain_name
}

output "cloudfront_distribution_id" {
  description = "CloudFront distribution ID (for invalidation)"
  value       = module.storage.cloudfront_distribution_id
}

output "frontend_bucket" {
  description = "S3 bucket name for frontend deployment"
  value       = module.storage.frontend_bucket_name
}

output "ecr_api_url" {
  description = "ECR repository URL for the API image"
  value       = module.ecr.api_repository_url
}

output "ecr_worker_url" {
  description = "ECR repository URL for the worker image"
  value       = module.ecr.worker_repository_url
}

output "rds_endpoint" {
  description = "RDS endpoint"
  value       = module.rds.endpoint
}

output "redis_endpoint" {
  description = "ElastiCache Redis endpoint"
  value       = module.redis.primary_endpoint
}
