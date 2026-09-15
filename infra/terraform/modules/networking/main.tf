# Evidentia — Networking Module
#
# Creates the VPC, subnets, NAT Gateway, Internet Gateway, route tables,
# and security groups for the Evidentia AWS deployment.
#
# Architecture:
#   Public subnets:  ALB, NAT Gateway
#   Private subnets: ECS (API, Worker)
#   Data subnets:    RDS, ElastiCache, MinIO EC2, Fabric EC2

terraform {
  required_version = ">= 1.5"
}

# ---------------------------------------------------------------------------
# Variables
# ---------------------------------------------------------------------------

variable "project" {
  description = "Project name for resource naming and tagging"
  type        = string
  default     = "evidentia"
}

variable "environment" {
  description = "Deployment environment (dev, staging, production)"
  type        = string
}

variable "aws_region" {
  description = "AWS region"
  type        = string
}

variable "vpc_cidr" {
  description = "CIDR block for the VPC"
  type        = string
  default     = "10.0.0.0/16"
}

variable "availability_zones" {
  description = "List of availability zones to use"
  type        = list(string)
}

# ---------------------------------------------------------------------------
# VPC
# ---------------------------------------------------------------------------

resource "aws_vpc" "main" {
  cidr_block           = var.vpc_cidr
  enable_dns_support   = true
  enable_dns_hostnames = true

  tags = {
    Name        = "${var.project}-${var.environment}-vpc"
    Project     = var.project
    Environment = var.environment
    ManagedBy   = "terraform"
  }
}

# ---------------------------------------------------------------------------
# Internet Gateway
# ---------------------------------------------------------------------------

resource "aws_internet_gateway" "main" {
  vpc_id = aws_vpc.main.id

  tags = {
    Name        = "${var.project}-${var.environment}-igw"
    Project     = var.project
    Environment = var.environment
    ManagedBy   = "terraform"
  }
}

# ---------------------------------------------------------------------------
# Subnets
# ---------------------------------------------------------------------------

# Public subnets — ALB, NAT Gateway
resource "aws_subnet" "public" {
  count                   = length(var.availability_zones)
  vpc_id                  = aws_vpc.main.id
  cidr_block              = cidrsubnet(var.vpc_cidr, 8, count.index)
  availability_zone       = var.availability_zones[count.index]
  map_public_ip_on_launch = true

  tags = {
    Name        = "${var.project}-${var.environment}-public-${var.availability_zones[count.index]}"
    Project     = var.project
    Environment = var.environment
    Tier        = "public"
    ManagedBy   = "terraform"
  }
}

# Private application subnets — ECS API, ECS Worker
resource "aws_subnet" "private" {
  count             = length(var.availability_zones)
  vpc_id            = aws_vpc.main.id
  cidr_block        = cidrsubnet(var.vpc_cidr, 8, count.index + 10)
  availability_zone = var.availability_zones[count.index]

  tags = {
    Name        = "${var.project}-${var.environment}-private-${var.availability_zones[count.index]}"
    Project     = var.project
    Environment = var.environment
    Tier        = "private"
    ManagedBy   = "terraform"
  }
}

# Data subnets — RDS, ElastiCache, MinIO EC2, Fabric EC2
resource "aws_subnet" "data" {
  count             = length(var.availability_zones)
  vpc_id            = aws_vpc.main.id
  cidr_block        = cidrsubnet(var.vpc_cidr, 8, count.index + 20)
  availability_zone = var.availability_zones[count.index]

  tags = {
    Name        = "${var.project}-${var.environment}-data-${var.availability_zones[count.index]}"
    Project     = var.project
    Environment = var.environment
    Tier        = "data"
    ManagedBy   = "terraform"
  }
}

# ---------------------------------------------------------------------------
# NAT Gateway (single, for cost optimization in demo)
# ---------------------------------------------------------------------------

resource "aws_eip" "nat" {
  domain = "vpc"

  tags = {
    Name        = "${var.project}-${var.environment}-nat-eip"
    Project     = var.project
    Environment = var.environment
    ManagedBy   = "terraform"
  }
}

resource "aws_nat_gateway" "main" {
  allocation_id = aws_eip.nat.id
  subnet_id     = aws_subnet.public[0].id

  tags = {
    Name        = "${var.project}-${var.environment}-nat"
    Project     = var.project
    Environment = var.environment
    ManagedBy   = "terraform"
  }

  depends_on = [aws_internet_gateway.main]
}

# ---------------------------------------------------------------------------
# Route Tables
# ---------------------------------------------------------------------------

# Public route table — routes to Internet Gateway
resource "aws_route_table" "public" {
  vpc_id = aws_vpc.main.id

  route {
    cidr_block = "0.0.0.0/0"
    gateway_id = aws_internet_gateway.main.id
  }

  tags = {
    Name        = "${var.project}-${var.environment}-public-rt"
    Project     = var.project
    Environment = var.environment
    ManagedBy   = "terraform"
  }
}

resource "aws_route_table_association" "public" {
  count          = length(var.availability_zones)
  subnet_id      = aws_subnet.public[count.index].id
  route_table_id = aws_route_table.public.id
}

# Private route table — routes to NAT Gateway
resource "aws_route_table" "private" {
  vpc_id = aws_vpc.main.id

  route {
    cidr_block     = "0.0.0.0/0"
    nat_gateway_id = aws_nat_gateway.main.id
  }

  tags = {
    Name        = "${var.project}-${var.environment}-private-rt"
    Project     = var.project
    Environment = var.environment
    ManagedBy   = "terraform"
  }
}

resource "aws_route_table_association" "private" {
  count          = length(var.availability_zones)
  subnet_id      = aws_subnet.private[count.index].id
  route_table_id = aws_route_table.private.id
}

resource "aws_route_table_association" "data" {
  count          = length(var.availability_zones)
  subnet_id      = aws_subnet.data[count.index].id
  route_table_id = aws_route_table.private.id
}

# ---------------------------------------------------------------------------
# Security Groups
# ---------------------------------------------------------------------------

# ALB — accepts 80/443 from the Internet
resource "aws_security_group" "alb" {
  name_prefix = "${var.project}-${var.environment}-alb-"
  vpc_id      = aws_vpc.main.id
  description = "ALB: HTTP/HTTPS from Internet"

  ingress {
    description = "HTTP"
    from_port   = 80
    to_port     = 80
    protocol    = "tcp"
    cidr_blocks = ["0.0.0.0/0"]
  }

  ingress {
    description = "HTTPS"
    from_port   = 443
    to_port     = 443
    protocol    = "tcp"
    cidr_blocks = ["0.0.0.0/0"]
  }

  egress {
    description = "All outbound"
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }

  tags = {
    Name        = "${var.project}-${var.environment}-alb-sg"
    Project     = var.project
    Environment = var.environment
    Component   = "alb"
    ManagedBy   = "terraform"
  }

  lifecycle {
    create_before_destroy = true
  }
}

# ECS tasks — accepts 8080 from ALB only
resource "aws_security_group" "ecs" {
  name_prefix = "${var.project}-${var.environment}-ecs-"
  vpc_id      = aws_vpc.main.id
  description = "ECS tasks: API port from ALB, outbound to dependencies"

  ingress {
    description     = "API from ALB"
    from_port       = 8080
    to_port         = 8080
    protocol        = "tcp"
    security_groups = [aws_security_group.alb.id]
  }

  egress {
    description = "All outbound (RDS, Redis, MinIO, Fabric, ECR, etc.)"
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }

  tags = {
    Name        = "${var.project}-${var.environment}-ecs-sg"
    Project     = var.project
    Environment = var.environment
    Component   = "ecs"
    ManagedBy   = "terraform"
  }

  lifecycle {
    create_before_destroy = true
  }
}

# RDS — accepts 5432 from ECS only
resource "aws_security_group" "rds" {
  name_prefix = "${var.project}-${var.environment}-rds-"
  vpc_id      = aws_vpc.main.id
  description = "RDS PostgreSQL: 5432 from ECS tasks only"

  ingress {
    description     = "PostgreSQL from ECS"
    from_port       = 5432
    to_port         = 5432
    protocol        = "tcp"
    security_groups = [aws_security_group.ecs.id]
  }

  egress {
    description = "All outbound"
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }

  tags = {
    Name        = "${var.project}-${var.environment}-rds-sg"
    Project     = var.project
    Environment = var.environment
    Component   = "database"
    ManagedBy   = "terraform"
  }

  lifecycle {
    create_before_destroy = true
  }
}

# ElastiCache — accepts 6379 from ECS only
resource "aws_security_group" "redis" {
  name_prefix = "${var.project}-${var.environment}-redis-"
  vpc_id      = aws_vpc.main.id
  description = "ElastiCache Redis: 6379 from ECS tasks only"

  ingress {
    description     = "Redis from ECS"
    from_port       = 6379
    to_port         = 6379
    protocol        = "tcp"
    security_groups = [aws_security_group.ecs.id]
  }

  egress {
    description = "All outbound"
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }

  tags = {
    Name        = "${var.project}-${var.environment}-redis-sg"
    Project     = var.project
    Environment = var.environment
    Component   = "redis"
    ManagedBy   = "terraform"
  }

  lifecycle {
    create_before_destroy = true
  }
}

# MinIO EC2 — accepts 9000 from ECS only
resource "aws_security_group" "minio" {
  name_prefix = "${var.project}-${var.environment}-minio-"
  vpc_id      = aws_vpc.main.id
  description = "MinIO: S3 API port from ECS tasks only"

  ingress {
    description     = "MinIO S3 API from ECS"
    from_port       = 9000
    to_port         = 9000
    protocol        = "tcp"
    security_groups = [aws_security_group.ecs.id]
  }

  # SSH from bastion for admin access (optional)
  ingress {
    description = "SSH from VPC"
    from_port   = 22
    to_port     = 22
    protocol    = "tcp"
    cidr_blocks = [var.vpc_cidr]
  }

  egress {
    description = "All outbound"
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }

  tags = {
    Name        = "${var.project}-${var.environment}-minio-sg"
    Project     = var.project
    Environment = var.environment
    Component   = "storage"
    ManagedBy   = "terraform"
  }

  lifecycle {
    create_before_destroy = true
  }
}

# Fabric EC2 — peer/orderer/CA ports from ECS only
resource "aws_security_group" "fabric" {
  name_prefix = "${var.project}-${var.environment}-fabric-"
  vpc_id      = aws_vpc.main.id
  description = "Hyperledger Fabric: peer/orderer/CA from ECS tasks only"

  # Fabric peer gRPC
  ingress {
    description     = "Fabric peer from ECS"
    from_port       = 7051
    to_port         = 7051
    protocol        = "tcp"
    security_groups = [aws_security_group.ecs.id]
  }

  # Fabric orderer
  ingress {
    description     = "Fabric orderer from ECS"
    from_port       = 7050
    to_port         = 7050
    protocol        = "tcp"
    security_groups = [aws_security_group.ecs.id]
  }

  # Fabric CA
  ingress {
    description     = "Fabric CA from ECS"
    from_port       = 7054
    to_port         = 7054
    protocol        = "tcp"
    security_groups = [aws_security_group.ecs.id]
  }

  # Internal Fabric peer communication
  ingress {
    description = "Fabric internal"
    from_port   = 7050
    to_port     = 7060
    protocol    = "tcp"
    self        = true
  }

  # SSH from bastion for admin access
  ingress {
    description = "SSH from VPC"
    from_port   = 22
    to_port     = 22
    protocol    = "tcp"
    cidr_blocks = [var.vpc_cidr]
  }

  egress {
    description = "All outbound"
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }

  tags = {
    Name        = "${var.project}-${var.environment}-fabric-sg"
    Project     = var.project
    Environment = var.environment
    Component   = "blockchain"
    ManagedBy   = "terraform"
  }

  lifecycle {
    create_before_destroy = true
  }
}

# ---------------------------------------------------------------------------
# Outputs
# ---------------------------------------------------------------------------

output "vpc_id" {
  value = aws_vpc.main.id
}

output "public_subnet_ids" {
  value = aws_subnet.public[*].id
}

output "private_subnet_ids" {
  value = aws_subnet.private[*].id
}

output "data_subnet_ids" {
  value = aws_subnet.data[*].id
}

output "alb_security_group_id" {
  value = aws_security_group.alb.id
}

output "ecs_security_group_id" {
  value = aws_security_group.ecs.id
}

output "rds_security_group_id" {
  value = aws_security_group.rds.id
}

output "redis_security_group_id" {
  value = aws_security_group.redis.id
}

output "minio_security_group_id" {
  value = aws_security_group.minio.id
}

output "fabric_security_group_id" {
  value = aws_security_group.fabric.id
}
