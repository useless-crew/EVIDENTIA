# Evidentia — RDS PostgreSQL Module

terraform {
  required_version = ">= 1.5"
}

variable "project" {
  type    = string
  default = "evidentia"
}

variable "environment" {
  type = string
}

variable "subnet_ids" {
  description = "Data subnet IDs for the DB subnet group"
  type        = list(string)
}

variable "security_group_id" {
  description = "Security group ID for RDS"
  type        = string
}

variable "instance_class" {
  description = "RDS instance class"
  type        = string
  default     = "db.t3.micro"
}

variable "allocated_storage" {
  description = "Storage in GB"
  type        = number
  default     = 20
}

variable "db_name" {
  description = "Database name"
  type        = string
  default     = "evidentia"
}

variable "db_username" {
  description = "Master username (schema owner, not the app runtime role)"
  type        = string
  default     = "evidentia"
}

variable "db_password" {
  description = "Master password — supply via terraform.tfvars or TF_VAR_db_password, never in source"
  type        = string
  sensitive   = true
}

variable "backup_retention_period" {
  description = "Days to retain automated backups"
  type        = number
  default     = 7
}

variable "multi_az" {
  description = "Enable Multi-AZ (recommended for production, not needed for demo)"
  type        = bool
  default     = false
}

# ---------------------------------------------------------------------------
# DB Subnet Group
# ---------------------------------------------------------------------------

resource "aws_db_subnet_group" "main" {
  name       = "${var.project}-${var.environment}-db-subnet"
  subnet_ids = var.subnet_ids

  tags = {
    Project     = var.project
    Environment = var.environment
    Component   = "database"
    ManagedBy   = "terraform"
  }
}

# ---------------------------------------------------------------------------
# RDS Instance
# ---------------------------------------------------------------------------

resource "aws_db_instance" "main" {
  identifier = "${var.project}-${var.environment}-postgres"

  engine         = "postgres"
  engine_version = "15"
  instance_class = var.instance_class

  allocated_storage     = var.allocated_storage
  max_allocated_storage = var.allocated_storage * 2
  storage_type          = "gp3"
  storage_encrypted     = true

  db_name  = var.db_name
  username = var.db_username
  password = var.db_password

  db_subnet_group_name   = aws_db_subnet_group.main.name
  vpc_security_group_ids = [var.security_group_id]

  multi_az            = var.multi_az
  publicly_accessible = false

  backup_retention_period = var.backup_retention_period
  backup_window           = "03:00-04:00"
  maintenance_window      = "Mon:04:00-Mon:05:00"

  # Do not automatically destroy the database on terraform destroy.
  # Require explicit snapshot or skip.
  skip_final_snapshot       = var.environment != "production"
  final_snapshot_identifier = var.environment == "production" ? "${var.project}-${var.environment}-final-snapshot" : null
  deletion_protection       = var.environment == "production"

  # Performance Insights (free tier available)
  performance_insights_enabled = true

  # Parameter group — use PostgreSQL defaults; Evidentia's RLS/RBAC/audit
  # chain work with standard settings.
  parameter_group_name = "default.postgres15"

  tags = {
    Project     = var.project
    Environment = var.environment
    Component   = "database"
    ManagedBy   = "terraform"
  }
}

# ---------------------------------------------------------------------------
# Outputs
# ---------------------------------------------------------------------------

output "endpoint" {
  value = aws_db_instance.main.endpoint
}

output "address" {
  value = aws_db_instance.main.address
}

output "port" {
  value = aws_db_instance.main.port
}

output "db_name" {
  value = aws_db_instance.main.db_name
}
