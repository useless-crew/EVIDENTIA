# Evidentia — ElastiCache Redis Module

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
  description = "Data subnet IDs for the ElastiCache subnet group"
  type        = list(string)
}

variable "security_group_id" {
  description = "Security group ID for Redis"
  type        = string
}

variable "node_type" {
  description = "ElastiCache node type"
  type        = string
  default     = "cache.t3.micro"
}

variable "auth_token" {
  description = "Redis AUTH token — supply securely, never in source"
  type        = string
  sensitive   = true
  default     = null
}

# ---------------------------------------------------------------------------
# Subnet Group
# ---------------------------------------------------------------------------

resource "aws_elasticache_subnet_group" "main" {
  name       = "${var.project}-${var.environment}-redis-subnet"
  subnet_ids = var.subnet_ids

  tags = {
    Project     = var.project
    Environment = var.environment
    Component   = "redis"
    ManagedBy   = "terraform"
  }
}

# ---------------------------------------------------------------------------
# ElastiCache Redis (single node for demo, cluster mode for production)
# ---------------------------------------------------------------------------

resource "aws_elasticache_replication_group" "main" {
  replication_group_id = "${var.project}-${var.environment}-redis"
  description          = "Evidentia ${var.environment} Redis (Asynq, rate limiting, SSE pub/sub)"

  engine         = "redis"
  engine_version = "7.0"
  node_type      = var.node_type

  # Single node for demo; set num_cache_clusters > 1 for HA
  num_cache_clusters = 1

  port                = 6379
  subnet_group_name   = aws_elasticache_subnet_group.main.name
  security_group_ids  = [var.security_group_id]

  # TLS in transit — required, the Go backend's REDIS_TLS=true enables this
  transit_encryption_enabled = true

  # Auth token (password) — maps to the backend's REDIS_PASSWORD
  auth_token = var.auth_token

  # At-rest encryption
  at_rest_encryption_enabled = true

  # Maintenance
  maintenance_window       = "Mon:05:00-Mon:06:00"
  snapshot_retention_limit = 1
  snapshot_window          = "04:00-05:00"

  # noeviction is critical for Asynq queue safety (see docker-compose.prod.yml)
  parameter_group_name = "default.redis7"

  automatic_failover_enabled = false # single-node demo; true for production HA

  tags = {
    Project     = var.project
    Environment = var.environment
    Component   = "redis"
    ManagedBy   = "terraform"
  }
}

# ---------------------------------------------------------------------------
# Outputs
# ---------------------------------------------------------------------------

output "primary_endpoint" {
  value = aws_elasticache_replication_group.main.primary_endpoint_address
}

output "port" {
  value = aws_elasticache_replication_group.main.port
}

output "redis_addr" {
  description = "REDIS_ADDR value for the backend (host:port)"
  value       = "${aws_elasticache_replication_group.main.primary_endpoint_address}:${aws_elasticache_replication_group.main.port}"
}
