# Evidentia — ECS Module
#
# ECS Fargate cluster, task definitions, services, and ALB for the
# Evidentia API and Asynq worker.

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

variable "aws_region" {
  type = string
}

variable "vpc_id" {
  type = string
}

variable "public_subnet_ids" {
  type = list(string)
}

variable "private_subnet_ids" {
  type = list(string)
}

variable "alb_security_group_id" {
  type = string
}

variable "ecs_security_group_id" {
  type = string
}

variable "execution_role_arn" {
  type = string
}

variable "api_task_role_arn" {
  type = string
}

variable "worker_task_role_arn" {
  type = string
}

variable "api_image" {
  description = "Full ECR image URI for the API (e.g. 123456789.dkr.ecr.ap-south-1.amazonaws.com/evidentia-api:sha-abc123)"
  type        = string
}

variable "worker_image" {
  description = "Full ECR image URI for the worker"
  type        = string
}

variable "api_cpu" {
  type    = number
  default = 512
}

variable "api_memory" {
  type    = number
  default = 1024
}

variable "worker_cpu" {
  type    = number
  default = 256
}

variable "worker_memory" {
  type    = number
  default = 512
}

variable "api_desired_count" {
  type    = number
  default = 1
}

variable "acm_certificate_arn" {
  description = "ACM certificate ARN for HTTPS on the ALB"
  type        = string
  default     = ""
}

variable "environment_variables" {
  description = "Map of non-secret environment variables for ECS tasks"
  type        = map(string)
  default     = {}
}

variable "secrets" {
  description = "Map of name -> Secrets Manager ARN for sensitive values"
  type        = map(string)
  default     = {}
}

# ---------------------------------------------------------------------------
# ECS Cluster
# ---------------------------------------------------------------------------

resource "aws_ecs_cluster" "main" {
  name = "${var.project}-${var.environment}"

  setting {
    name  = "containerInsights"
    value = "enabled"
  }

  tags = {
    Project     = var.project
    Environment = var.environment
    ManagedBy   = "terraform"
  }
}

# ---------------------------------------------------------------------------
# CloudWatch Log Groups
# ---------------------------------------------------------------------------

resource "aws_cloudwatch_log_group" "api" {
  name              = "/ecs/${var.project}-${var.environment}/api"
  retention_in_days = 30

  tags = {
    Project     = var.project
    Environment = var.environment
    Component   = "api"
    ManagedBy   = "terraform"
  }
}

resource "aws_cloudwatch_log_group" "worker" {
  name              = "/ecs/${var.project}-${var.environment}/worker"
  retention_in_days = 30

  tags = {
    Project     = var.project
    Environment = var.environment
    Component   = "worker"
    ManagedBy   = "terraform"
  }
}

# ---------------------------------------------------------------------------
# Task Definitions
# ---------------------------------------------------------------------------

locals {
  # Build environment variables list for container definitions
  env_vars = [for k, v in var.environment_variables : { name = k, value = v }]
  secrets_list = [for k, v in var.secrets : { name = k, valueFrom = v }]
}

resource "aws_ecs_task_definition" "api" {
  family                   = "${var.project}-${var.environment}-api"
  requires_compatibilities = ["FARGATE"]
  network_mode             = "awsvpc"
  cpu                      = var.api_cpu
  memory                   = var.api_memory
  execution_role_arn       = var.execution_role_arn
  task_role_arn            = var.api_task_role_arn

  container_definitions = jsonencode([
    {
      name      = "api"
      image     = var.api_image
      essential = true

      portMappings = [
        {
          containerPort = 8080
          protocol      = "tcp"
        }
      ]

      environment = concat(local.env_vars, [
        { name = "DISABLE_EMBEDDED_WORKER", value = "true" },
        { name = "SERVER_HOST", value = "0.0.0.0" },
        { name = "SERVER_PORT", value = "8080" },
      ])

      secrets = local.secrets_list

      healthCheck = {
        command     = ["CMD-SHELL", "wget -qO- http://localhost:8080/health || exit 1"]
        interval    = 30
        timeout     = 5
        retries     = 3
        startPeriod = 30
      }

      logConfiguration = {
        logDriver = "awslogs"
        options = {
          "awslogs-group"         = aws_cloudwatch_log_group.api.name
          "awslogs-region"        = var.aws_region
          "awslogs-stream-prefix" = "api"
        }
      }
    }
  ])

  tags = {
    Project     = var.project
    Environment = var.environment
    Component   = "api"
    ManagedBy   = "terraform"
  }
}

resource "aws_ecs_task_definition" "worker" {
  family                   = "${var.project}-${var.environment}-worker"
  requires_compatibilities = ["FARGATE"]
  network_mode             = "awsvpc"
  cpu                      = var.worker_cpu
  memory                   = var.worker_memory
  execution_role_arn       = var.execution_role_arn
  task_role_arn            = var.worker_task_role_arn

  container_definitions = jsonencode([
    {
      name       = "worker"
      image      = var.worker_image
      essential  = true
      entryPoint = ["/app/worker"]

      environment = local.env_vars
      secrets     = local.secrets_list

      logConfiguration = {
        logDriver = "awslogs"
        options = {
          "awslogs-group"         = aws_cloudwatch_log_group.worker.name
          "awslogs-region"        = var.aws_region
          "awslogs-stream-prefix" = "worker"
        }
      }
    }
  ])

  tags = {
    Project     = var.project
    Environment = var.environment
    Component   = "worker"
    ManagedBy   = "terraform"
  }
}

# ---------------------------------------------------------------------------
# ALB
# ---------------------------------------------------------------------------

resource "aws_lb" "main" {
  name               = "${var.project}-${var.environment}-alb"
  internal           = false
  load_balancer_type = "application"
  security_groups    = [var.alb_security_group_id]
  subnets            = var.public_subnet_ids

  tags = {
    Project     = var.project
    Environment = var.environment
    Component   = "alb"
    ManagedBy   = "terraform"
  }
}

resource "aws_lb_target_group" "api" {
  name        = "${var.project}-${var.environment}-api-tg"
  port        = 8080
  protocol    = "HTTP"
  vpc_id      = var.vpc_id
  target_type = "ip"

  health_check {
    path                = "/health"
    port                = "traffic-port"
    healthy_threshold   = 2
    unhealthy_threshold = 3
    timeout             = 5
    interval            = 30
    matcher             = "200"
  }

  # Deregistration delay — allow in-flight requests to complete
  deregistration_delay = 30

  tags = {
    Project     = var.project
    Environment = var.environment
    Component   = "api"
    ManagedBy   = "terraform"
  }
}

# HTTP listener — redirect to HTTPS
resource "aws_lb_listener" "http" {
  load_balancer_arn = aws_lb.main.arn
  port              = 80
  protocol          = "HTTP"

  default_action {
    type = "redirect"
    redirect {
      port        = "443"
      protocol    = "HTTPS"
      status_code = "HTTP_301"
    }
  }
}

# HTTPS listener
resource "aws_lb_listener" "https" {
  count             = var.acm_certificate_arn != "" ? 1 : 0
  load_balancer_arn = aws_lb.main.arn
  port              = 443
  protocol          = "HTTPS"
  ssl_policy        = "ELBSecurityPolicy-TLS13-1-2-2021-06"
  certificate_arn   = var.acm_certificate_arn

  default_action {
    type             = "forward"
    target_group_arn = aws_lb_target_group.api.arn
  }
}

# ---------------------------------------------------------------------------
# ECS Services
# ---------------------------------------------------------------------------

resource "aws_ecs_service" "api" {
  name            = "${var.project}-api"
  cluster         = aws_ecs_cluster.main.id
  task_definition = aws_ecs_task_definition.api.arn
  desired_count   = var.api_desired_count
  launch_type     = "FARGATE"

  network_configuration {
    subnets          = var.private_subnet_ids
    security_groups  = [var.ecs_security_group_id]
    assign_public_ip = false
  }

  load_balancer {
    target_group_arn = aws_lb_target_group.api.arn
    container_name   = "api"
    container_port   = 8080
  }

  # Allow ECS to drain connections during deployment
  deployment_configuration {
    maximum_percent         = 200
    minimum_healthy_percent = 100
  }

  # Wait for ALB target to be healthy before considering deployment done
  health_check_grace_period_seconds = 60

  tags = {
    Project     = var.project
    Environment = var.environment
    Component   = "api"
    ManagedBy   = "terraform"
  }

  lifecycle {
    ignore_changes = [task_definition]  # Updated by CI/CD, not Terraform
  }
}

resource "aws_ecs_service" "worker" {
  name            = "${var.project}-worker"
  cluster         = aws_ecs_cluster.main.id
  task_definition = aws_ecs_task_definition.worker.arn
  desired_count   = 1
  launch_type     = "FARGATE"

  network_configuration {
    subnets          = var.private_subnet_ids
    security_groups  = [var.ecs_security_group_id]
    assign_public_ip = false
  }

  tags = {
    Project     = var.project
    Environment = var.environment
    Component   = "worker"
    ManagedBy   = "terraform"
  }

  lifecycle {
    ignore_changes = [task_definition]
  }
}

# ---------------------------------------------------------------------------
# Outputs
# ---------------------------------------------------------------------------

output "cluster_name" {
  value = aws_ecs_cluster.main.name
}

output "cluster_arn" {
  value = aws_ecs_cluster.main.arn
}

output "alb_dns_name" {
  value = aws_lb.main.dns_name
}

output "alb_zone_id" {
  value = aws_lb.main.zone_id
}

output "alb_arn" {
  value = aws_lb.main.arn
}

output "api_service_name" {
  value = aws_ecs_service.api.name
}

output "worker_service_name" {
  value = aws_ecs_service.worker.name
}

output "api_task_definition_arn" {
  value = aws_ecs_task_definition.api.arn
}

output "worker_task_definition_arn" {
  value = aws_ecs_task_definition.worker.arn
}
