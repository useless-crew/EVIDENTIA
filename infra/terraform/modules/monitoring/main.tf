# Evidentia — Monitoring Module (CloudWatch Alarms)

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

variable "ecs_cluster_name" {
  type = string
}

variable "api_service_name" {
  type = string
}

variable "worker_service_name" {
  type = string
}

variable "alb_arn_suffix" {
  description = "ALB ARN suffix for CloudWatch metrics"
  type        = string
  default     = ""
}

variable "rds_instance_id" {
  description = "RDS instance identifier"
  type        = string
  default     = ""
}

variable "alarm_sns_topic_arn" {
  description = "SNS topic ARN for alarm notifications"
  type        = string
  default     = ""
}

locals {
  alarm_actions = var.alarm_sns_topic_arn != "" ? [var.alarm_sns_topic_arn] : []
}

# ---------------------------------------------------------------------------
# ECS Alarms
# ---------------------------------------------------------------------------

# API service — high CPU
resource "aws_cloudwatch_metric_alarm" "api_cpu_high" {
  alarm_name          = "${var.project}-${var.environment}-api-cpu-high"
  comparison_operator = "GreaterThanThreshold"
  evaluation_periods  = 3
  metric_name         = "CPUUtilization"
  namespace           = "AWS/ECS"
  period              = 300
  statistic           = "Average"
  threshold           = 80
  alarm_description   = "API CPU utilization > 80% for 15 minutes"
  alarm_actions       = local.alarm_actions

  dimensions = {
    ClusterName = var.ecs_cluster_name
    ServiceName = var.api_service_name
  }

  tags = {
    Project     = var.project
    Environment = var.environment
    Component   = "monitoring"
    ManagedBy   = "terraform"
  }
}

# API service — high memory
resource "aws_cloudwatch_metric_alarm" "api_memory_high" {
  alarm_name          = "${var.project}-${var.environment}-api-memory-high"
  comparison_operator = "GreaterThanThreshold"
  evaluation_periods  = 3
  metric_name         = "MemoryUtilization"
  namespace           = "AWS/ECS"
  period              = 300
  statistic           = "Average"
  threshold           = 85
  alarm_description   = "API memory utilization > 85% for 15 minutes"
  alarm_actions       = local.alarm_actions

  dimensions = {
    ClusterName = var.ecs_cluster_name
    ServiceName = var.api_service_name
  }

  tags = {
    Project     = var.project
    Environment = var.environment
    Component   = "monitoring"
    ManagedBy   = "terraform"
  }
}

# Worker — running task count drops to zero
resource "aws_cloudwatch_metric_alarm" "worker_running_count" {
  alarm_name          = "${var.project}-${var.environment}-worker-no-tasks"
  comparison_operator = "LessThanThreshold"
  evaluation_periods  = 2
  metric_name         = "RunningTaskCount"
  namespace           = "ECS/ContainerInsights"
  period              = 300
  statistic           = "Average"
  threshold           = 1
  alarm_description   = "Worker has no running tasks for 10 minutes — Asynq jobs are not being processed"
  alarm_actions       = local.alarm_actions

  dimensions = {
    ClusterName = var.ecs_cluster_name
    ServiceName = var.worker_service_name
  }

  tags = {
    Project     = var.project
    Environment = var.environment
    Component   = "monitoring"
    ManagedBy   = "terraform"
  }
}

# ---------------------------------------------------------------------------
# ALB Alarms (only created when ALB ARN suffix is provided)
# ---------------------------------------------------------------------------

resource "aws_cloudwatch_metric_alarm" "alb_5xx" {
  count               = var.alb_arn_suffix != "" ? 1 : 0
  alarm_name          = "${var.project}-${var.environment}-alb-5xx-high"
  comparison_operator = "GreaterThanThreshold"
  evaluation_periods  = 2
  metric_name         = "HTTPCode_Target_5XX_Count"
  namespace           = "AWS/ApplicationELB"
  period              = 300
  statistic           = "Sum"
  threshold           = 10
  alarm_description   = "More than 10 5xx errors from backend in 5 minutes"
  alarm_actions       = local.alarm_actions
  treat_missing_data  = "notBreaching"

  dimensions = {
    LoadBalancer = var.alb_arn_suffix
  }

  tags = {
    Project     = var.project
    Environment = var.environment
    Component   = "monitoring"
    ManagedBy   = "terraform"
  }
}

resource "aws_cloudwatch_metric_alarm" "alb_latency" {
  count               = var.alb_arn_suffix != "" ? 1 : 0
  alarm_name          = "${var.project}-${var.environment}-alb-latency-high"
  comparison_operator = "GreaterThanThreshold"
  evaluation_periods  = 3
  metric_name         = "TargetResponseTime"
  namespace           = "AWS/ApplicationELB"
  period              = 300
  statistic           = "p95"
  threshold           = 5
  alarm_description   = "p95 API latency > 5 seconds for 15 minutes"
  alarm_actions       = local.alarm_actions
  treat_missing_data  = "notBreaching"

  dimensions = {
    LoadBalancer = var.alb_arn_suffix
  }

  tags = {
    Project     = var.project
    Environment = var.environment
    Component   = "monitoring"
    ManagedBy   = "terraform"
  }
}

# ---------------------------------------------------------------------------
# RDS Alarms (only created when RDS instance ID is provided)
# ---------------------------------------------------------------------------

resource "aws_cloudwatch_metric_alarm" "rds_cpu" {
  count               = var.rds_instance_id != "" ? 1 : 0
  alarm_name          = "${var.project}-${var.environment}-rds-cpu-high"
  comparison_operator = "GreaterThanThreshold"
  evaluation_periods  = 3
  metric_name         = "CPUUtilization"
  namespace           = "AWS/RDS"
  period              = 300
  statistic           = "Average"
  threshold           = 80
  alarm_description   = "RDS CPU > 80% for 15 minutes"
  alarm_actions       = local.alarm_actions

  dimensions = {
    DBInstanceIdentifier = var.rds_instance_id
  }

  tags = {
    Project     = var.project
    Environment = var.environment
    Component   = "monitoring"
    ManagedBy   = "terraform"
  }
}

resource "aws_cloudwatch_metric_alarm" "rds_connections" {
  count               = var.rds_instance_id != "" ? 1 : 0
  alarm_name          = "${var.project}-${var.environment}-rds-connections-high"
  comparison_operator = "GreaterThanThreshold"
  evaluation_periods  = 2
  metric_name         = "DatabaseConnections"
  namespace           = "AWS/RDS"
  period              = 300
  statistic           = "Average"
  threshold           = 80
  alarm_description   = "RDS connections approaching limit (>80) — check ECS scaling and connection pool settings"
  alarm_actions       = local.alarm_actions

  dimensions = {
    DBInstanceIdentifier = var.rds_instance_id
  }

  tags = {
    Project     = var.project
    Environment = var.environment
    Component   = "monitoring"
    ManagedBy   = "terraform"
  }
}

resource "aws_cloudwatch_metric_alarm" "rds_free_storage" {
  count               = var.rds_instance_id != "" ? 1 : 0
  alarm_name          = "${var.project}-${var.environment}-rds-storage-low"
  comparison_operator = "LessThanThreshold"
  evaluation_periods  = 1
  metric_name         = "FreeStorageSpace"
  namespace           = "AWS/RDS"
  period              = 300
  statistic           = "Average"
  threshold           = 2147483648  # 2 GB
  alarm_description   = "RDS free storage < 2 GB"
  alarm_actions       = local.alarm_actions

  dimensions = {
    DBInstanceIdentifier = var.rds_instance_id
  }

  tags = {
    Project     = var.project
    Environment = var.environment
    Component   = "monitoring"
    ManagedBy   = "terraform"
  }
}
