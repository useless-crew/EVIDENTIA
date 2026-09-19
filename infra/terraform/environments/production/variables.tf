# Evidentia — Production Terraform Variables

variable "aws_region" {
  description = "AWS region for primary workloads"
  type        = string
  default     = "ap-south-1"
}

variable "environment" {
  description = "Environment identifier"
  type        = string
  default     = "production"
}

variable "aws_account_id" {
  description = "AWS Account ID"
  type        = string
}

variable "vpc_cidr" {
  description = "VPC CIDR block"
  type        = string
  default     = "10.0.0.0/16"
}

variable "availability_zones" {
  description = "List of availability zones across which subnets are deployed"
  type        = list(string)
  default     = ["ap-south-1a", "ap-south-1b", "ap-south-1c"]
}

# ---- Domain & Certificates ----
variable "domain_name" {
  description = "Base domain name (e.g. evidentia.example.com)"
  type        = string
}

variable "frontend_subdomain" {
  description = "Frontend domain (e.g. app.evidentia.example.com)"
  type        = string
  default     = "app"
}

variable "api_subdomain" {
  description = "API domain (e.g. api.evidentia.example.com)"
  type        = string
  default     = "api"
}

variable "alb_acm_certificate_arn" {
  description = "ACM Certificate ARN for the Application Load Balancer in the primary region"
  type        = string
}

variable "cloudfront_acm_certificate_arn" {
  description = "ACM Certificate ARN for CloudFront (must be issued in us-east-1)"
  type        = string
}

# ---- Container Images ----
variable "api_image" {
  description = "Full ECR image URI for the API container"
  type        = string
}

variable "worker_image" {
  description = "Full ECR image URI for the worker container"
  type        = string
}

# ---- Database & Cache Secrets ----
variable "db_password" {
  description = "Master password for Amazon RDS PostgreSQL"
  type        = string
  sensitive   = true
}

variable "redis_auth_token" {
  description = "Authentication token for Amazon ElastiCache Redis"
  type        = string
  sensitive   = true
}

variable "minio_root_password" {
  description = "Root secret key for MinIO"
  type        = string
  sensitive   = true
}

# ---- Secrets Manager ARNs ----
variable "jwt_secret_arn" {
  description = "Secrets Manager secret ARN for JWT_SIGNING_KEY"
  type        = string
}

variable "db_app_password_arn" {
  description = "Secrets Manager secret ARN for evidentia_app PostgreSQL password"
  type        = string
}

variable "redis_auth_token_arn" {
  description = "Secrets Manager secret ARN for Redis AUTH token"
  type        = string
}

variable "minio_secret_key_arn" {
  description = "Secrets Manager secret ARN for MINIO_SECRET_KEY"
  type        = string
}

variable "certificate_signing_key_arn" {
  description = "Secrets Manager secret ARN for CERTIFICATE_SIGNING_KEY (ECDSA private key)"
  type        = string
  default     = ""
}

# ---- Sizing & Scaling ----
variable "rds_instance_class" {
  description = "RDS instance class"
  type        = string
  default     = "db.t3.medium"
}

variable "rds_allocated_storage" {
  description = "RDS storage allocation in GB"
  type        = number
  default     = 100
}

variable "redis_node_type" {
  description = "ElastiCache node type"
  type        = string
  default     = "cache.t3.medium"
}

variable "api_desired_count" {
  description = "Desired number of ECS API Fargate tasks"
  type        = number
  default     = 2
}

variable "worker_desired_count" {
  description = "Desired number of ECS Worker Fargate tasks"
  type        = number
  default     = 2
}
