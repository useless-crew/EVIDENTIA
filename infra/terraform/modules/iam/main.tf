# Evidentia — IAM Module
#
# Least-privilege IAM roles for ECS tasks and CI/CD deployment.
# No AWS root credentials, no embedded access keys.

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

variable "aws_account_id" {
  type = string
}

variable "ecr_api_arn" {
  description = "ARN of the API ECR repository"
  type        = string
}

variable "ecr_worker_arn" {
  description = "ARN of the worker ECR repository"
  type        = string
}

variable "secrets_arns" {
  description = "List of Secrets Manager secret ARNs the ECS tasks may read"
  type        = list(string)
  default     = []
}

# ---------------------------------------------------------------------------
# ECS Task Execution Role (used by ECS agent to pull images, write logs)
# ---------------------------------------------------------------------------

data "aws_iam_policy_document" "ecs_assume" {
  statement {
    actions = ["sts:AssumeRole"]
    principals {
      type        = "Service"
      identifiers = ["ecs-tasks.amazonaws.com"]
    }
  }
}

resource "aws_iam_role" "ecs_execution" {
  name               = "${var.project}-${var.environment}-ecs-execution"
  assume_role_policy = data.aws_iam_policy_document.ecs_assume.json

  tags = {
    Project     = var.project
    Environment = var.environment
    Component   = "iam"
    ManagedBy   = "terraform"
  }
}

resource "aws_iam_role_policy_attachment" "ecs_execution_managed" {
  role       = aws_iam_role.ecs_execution.name
  policy_arn = "arn:aws:iam::aws:policy/service-role/AmazonECSTaskExecutionRolePolicy"
}

# Allow ECS execution role to read secrets from Secrets Manager
resource "aws_iam_role_policy" "ecs_execution_secrets" {
  count = length(var.secrets_arns) > 0 ? 1 : 0
  name  = "${var.project}-${var.environment}-ecs-secrets"
  role  = aws_iam_role.ecs_execution.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect   = "Allow"
        Action   = ["secretsmanager:GetSecretValue"]
        Resource = var.secrets_arns
      }
    ]
  })
}

# ---------------------------------------------------------------------------
# ECS Task Role — API (what the application container itself can do)
# ---------------------------------------------------------------------------

resource "aws_iam_role" "ecs_task_api" {
  name               = "${var.project}-${var.environment}-ecs-task-api"
  assume_role_policy = data.aws_iam_policy_document.ecs_assume.json

  tags = {
    Project     = var.project
    Environment = var.environment
    Component   = "api"
    ManagedBy   = "terraform"
  }
}

# API task needs no additional AWS permissions — it communicates with
# RDS/Redis/MinIO via standard TCP, not AWS APIs. If S3 direct access
# is ever needed (replacing MinIO), add a specific S3 policy here.

# ---------------------------------------------------------------------------
# ECS Task Role — Worker
# ---------------------------------------------------------------------------

resource "aws_iam_role" "ecs_task_worker" {
  name               = "${var.project}-${var.environment}-ecs-task-worker"
  assume_role_policy = data.aws_iam_policy_document.ecs_assume.json

  tags = {
    Project     = var.project
    Environment = var.environment
    Component   = "worker"
    ManagedBy   = "terraform"
  }
}

# ---------------------------------------------------------------------------
# Outputs
# ---------------------------------------------------------------------------

output "ecs_execution_role_arn" {
  value = aws_iam_role.ecs_execution.arn
}

output "ecs_task_api_role_arn" {
  value = aws_iam_role.ecs_task_api.arn
}

output "ecs_task_worker_role_arn" {
  value = aws_iam_role.ecs_task_worker.arn
}
