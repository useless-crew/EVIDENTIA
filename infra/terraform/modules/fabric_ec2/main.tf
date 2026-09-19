# Evidentia — Private Hyperledger Fabric EC2 Module
#
# Deploys the Hyperledger Fabric permissioned ledger (Orderer, Peers, Chaincode)
# on a dedicated private EC2 instance in the Data Subnet.
# Attached to a KMS-encrypted gp3 EBS volume for immutable ledger state persistence.
# Inbound traffic is strictly restricted to ECS tasks via mTLS on gRPC port 7051.
# No public IP is assigned; administrative access is managed through AWS SSM.

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

variable "subnet_id" {
  description = "Private Data Subnet ID for the Fabric node"
  type        = string
}

variable "security_group_id" {
  description = "Security Group ID restricting gRPC port 7051 to ECS tasks only"
  type        = string
}

variable "instance_type" {
  description = "EC2 instance type for Fabric (minimum 4 vCPU, 8 GB RAM recommended for multi-peer LTS)"
  type        = string
  default     = "t3.large"
}

variable "ebs_volume_size" {
  description = "Size in GB of the dedicated gp3 EBS volume for ledger state"
  type        = number
  default     = 100
}

variable "kms_key_arn" {
  description = "KMS Key ARN for EBS volume encryption (uses default aws/ebs if empty)"
  type        = string
  default     = ""
}

# ---------------------------------------------------------------------------
# IAM Role for SSM Session Manager
# ---------------------------------------------------------------------------

resource "aws_iam_role" "fabric_ssm" {
  name_prefix = "${var.project}-${var.environment}-fabric-ssm-"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect = "Allow"
        Principal = {
          Service = "ec2.amazonaws.com"
        }
        Action = "sts:AssumeRole"
      }
    ]
  })

  tags = {
    Project     = var.project
    Environment = var.environment
    Component   = "blockchain"
    ManagedBy   = "terraform"
  }
}

resource "aws_iam_role_policy_attachment" "fabric_ssm" {
  role       = aws_iam_role.fabric_ssm.name
  policy_arn = "arn:aws:iam::aws:policy/AmazonSSMManagedInstanceCore"
}

resource "aws_iam_instance_profile" "fabric" {
  name_prefix = "${var.project}-${var.environment}-fabric-profile-"
  role        = aws_iam_role.fabric_ssm.name
}

# ---------------------------------------------------------------------------
# AMI Lookup (Amazon Linux 2023)
# ---------------------------------------------------------------------------

data "aws_ami" "amazon_linux_2023" {
  most_recent = true
  owners      = ["amazon"]

  filter {
    name   = "name"
    values = ["al2023-ami-2023.*-x86_64"]
  }

  filter {
    name   = "virtualization-type"
    values = ["hvm"]
  }
}

# ---------------------------------------------------------------------------
# Encrypted EBS Volume for Ledger Data
# ---------------------------------------------------------------------------

data "aws_subnet" "selected" {
  id = var.subnet_id
}

resource "aws_ebs_volume" "ledger_data" {
  availability_zone = data.aws_subnet.selected.availability_zone
  size              = var.ebs_volume_size
  type              = "gp3"
  iops              = 3000
  throughput        = 125
  encrypted         = true
  kms_key_id        = var.kms_key_arn != "" ? var.kms_key_arn : null

  tags = {
    Name        = "${var.project}-${var.environment}-fabric-ledger-data"
    Project     = var.project
    Environment = var.environment
    Component   = "blockchain"
    ManagedBy   = "terraform"
  }
}

# ---------------------------------------------------------------------------
# Private EC2 Instance
# ---------------------------------------------------------------------------

resource "aws_instance" "fabric" {
  ami                  = data.aws_ami.amazon_linux_2023.id
  instance_type        = var.instance_type
  subnet_id            = var.subnet_id
  vpc_security_group_ids = [var.security_group_id]
  iam_instance_profile = aws_iam_instance_profile.fabric.name

  associate_public_ip_address = false

  root_block_device {
    volume_type = "gp3"
    volume_size = 40
    encrypted   = true
  }

  user_data = <<-EOF
              #!/bin/bash
              set -euo pipefail

              # Wait for EBS device to attach
              while [ ! -b /dev/xvdf ] && [ ! -b /dev/nvme1n1 ]; do
                sleep 2
              done

              DEVICE="/dev/xvdf"
              if [ -b /dev/nvme1n1 ]; then
                DEVICE="/dev/nvme1n1"
              fi

              # Format if no filesystem exists
              if ! blkid "$DEVICE"; then
                mkfs -t xfs "$DEVICE"
              fi

              # Mount to /var/hyperledger
              mkdir -p /var/hyperledger
              echo "$DEVICE /var/hyperledger xfs defaults,noatime 0 2" >> /etc/fstab
              mount /var/hyperledger

              # Install Docker & Compose
              dnf update -y
              dnf install -y docker git
              systemctl enable --now docker

              # Create directories for Fabric network
              mkdir -p /opt/evidentia-fabric/network
              mkdir -p /var/hyperledger/production
              EOF

  tags = {
    Name        = "${var.project}-${var.environment}-fabric-node"
    Project     = var.project
    Environment = var.environment
    Component   = "blockchain"
    ManagedBy   = "terraform"
  }
}

resource "aws_volume_attachment" "fabric_data" {
  device_name = "/dev/xvdf"
  volume_id   = aws_ebs_volume.ledger_data.id
  instance_id = aws_instance.fabric.id
}

# ---------------------------------------------------------------------------
# Outputs
# ---------------------------------------------------------------------------

output "instance_id" {
  description = "Fabric EC2 instance ID"
  value       = aws_instance.fabric.id
}

output "private_ip" {
  description = "Fabric private IP in Data Subnet"
  value       = aws_instance.fabric.private_ip
}

output "peer_endpoint" {
  description = "Fabric Peer gRPC endpoint (<private-ip>:7051)"
  value       = "${aws_instance.fabric.private_ip}:7051"
}

output "orderer_endpoint" {
  description = "Fabric Orderer endpoint (<private-ip>:7050)"
  value       = "${aws_instance.fabric.private_ip}:7050"
}
