# Evidentia — Private MinIO EC2 Module
#
# Deploys MinIO on a private EC2 instance in the Data Subnet.
# Attached to a dedicated, encrypted gp3 EBS volume for persistent evidence storage.
# Accessible ONLY from ECS API and Worker security groups on port 9000.
# No public IP is assigned; administrative access is via AWS Systems Manager (SSM).

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
  description = "Private Data Subnet ID for the MinIO instance"
  type        = string
}

variable "security_group_id" {
  description = "Security Group ID restricting port 9000 to ECS tasks only"
  type        = string
}

variable "instance_type" {
  description = "EC2 instance type"
  type        = string
  default     = "t3.medium"
}

variable "ebs_volume_size" {
  description = "Size in GB of the dedicated gp3 EBS volume for evidence storage"
  type        = number
  default     = 100
}

variable "kms_key_arn" {
  description = "KMS Key ARN for EBS volume encryption (uses default aws/ebs if empty)"
  type        = string
  default     = ""
}

variable "minio_root_user" {
  description = "MinIO root access key"
  type        = string
  sensitive   = true
}

variable "minio_root_password" {
  description = "MinIO root secret key (minimum 16 chars)"
  type        = string
  sensitive   = true
}

variable "minio_bucket" {
  description = "Primary evidence bucket name"
  type        = string
  default     = "evidentia-documents"
}

# ---------------------------------------------------------------------------
# IAM Role for SSM Session Manager
# ---------------------------------------------------------------------------

resource "aws_iam_role" "minio_ssm" {
  name_prefix = "${var.project}-${var.environment}-minio-ssm-"

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
    Component   = "storage"
    ManagedBy   = "terraform"
  }
}

resource "aws_iam_role_policy_attachment" "minio_ssm" {
  role       = aws_iam_role.minio_ssm.name
  policy_arn = "arn:aws:iam::aws:policy/AmazonSSMManagedInstanceCore"
}

resource "aws_iam_instance_profile" "minio" {
  name_prefix = "${var.project}-${var.environment}-minio-profile-"
  role        = aws_iam_role.minio_ssm.name
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
# Encrypted EBS Volume for Evidence Storage
# ---------------------------------------------------------------------------

data "aws_subnet" "selected" {
  id = var.subnet_id
}

resource "aws_ebs_volume" "evidence_data" {
  availability_zone = data.aws_subnet.selected.availability_zone
  size              = var.ebs_volume_size
  type              = "gp3"
  iops              = 3000
  throughput        = 125
  encrypted         = true
  kms_key_id        = var.kms_key_arn != "" ? var.kms_key_arn : null

  tags = {
    Name        = "${var.project}-${var.environment}-minio-evidence-data"
    Project     = var.project
    Environment = var.environment
    Component   = "storage"
    ManagedBy   = "terraform"
  }
}

# ---------------------------------------------------------------------------
# Private EC2 Instance
# ---------------------------------------------------------------------------

resource "aws_instance" "minio" {
  ami                  = data.aws_ami.amazon_linux_2023.id
  instance_type        = var.instance_type
  subnet_id            = var.subnet_id
  vpc_security_group_ids = [var.security_group_id]
  iam_instance_profile = aws_iam_instance_profile.minio.name

  associate_public_ip_address = false

  root_block_device {
    volume_type = "gp3"
    volume_size = 30
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

              # Mount to /data
              mkdir -p /data
              echo "$DEVICE /data xfs defaults,noatime 0 2" >> /etc/fstab
              mount /data

              # Install MinIO binary
              wget -q https://dl.min.io/server/minio/release/linux-amd64/minio -O /usr/local/bin/minio
              chmod +x /usr/local/bin/minio

              # Install MinIO Client (mc)
              wget -q https://dl.min.io/client/mc/release/linux-amd64/mc -O /usr/local/bin/mc
              chmod +x /usr/local/bin/mc

              # Create minio-user
              useradd -r minio-user -s /sbin/nologin || true
              chown -R minio-user:minio-user /data

              # Write MinIO environment file
              mkdir -p /etc/minio
              cat <<ENV > /etc/minio/minio.conf
              MINIO_ROOT_USER=${var.minio_root_user}
              MINIO_ROOT_PASSWORD=${var.minio_root_password}
              MINIO_VOLUMES="/data"
              MINIO_OPTS="--address :9000"
              ENV
              chmod 600 /etc/minio/minio.conf
              chown minio-user:minio-user /etc/minio/minio.conf

              # Create systemd service
              cat <<SERVICE > /etc/systemd/system/minio.service
              [Unit]
              Description=MinIO Object Storage
              Documentation=https://docs.min.io
              Wants=network-online.target
              After=network-online.target

              [Service]
              WorkingDirectory=/usr/local
              User=minio-user
              Group=minio-user
              EnvironmentFile=/etc/minio/minio.conf
              ExecStart=/usr/local/bin/minio server \$MINIO_OPTS \$MINIO_VOLUMES
              Restart=always
              LimitNOFILE=65536
              TasksMax=infinity
              TimeoutStopSec=infinity
              SendSIGKILL=no

              [Install]
              WantedBy=multi-user.target
              SERVICE

              systemctl daemon-reload
              systemctl enable --now minio

              # Wait for MinIO service to start and initialize bucket
              sleep 5
              /usr/local/bin/mc alias set local http://127.0.0.1:9000 "${var.minio_root_user}" "${var.minio_root_password}" || true
              /usr/local/bin/mc mb --ignore-existing local/${var.minio_bucket} || true
              /usr/local/bin/mc version enable local/${var.minio_bucket} || true
              EOF

  tags = {
    Name        = "${var.project}-${var.environment}-minio"
    Project     = var.project
    Environment = var.environment
    Component   = "storage"
    ManagedBy   = "terraform"
  }
}

resource "aws_volume_attachment" "minio_data" {
  device_name = "/dev/xvdf"
  volume_id   = aws_ebs_volume.evidence_data.id
  instance_id = aws_instance.minio.id
}

# ---------------------------------------------------------------------------
# Outputs
# ---------------------------------------------------------------------------

output "instance_id" {
  description = "MinIO EC2 instance ID"
  value       = aws_instance.minio.id
}

output "private_ip" {
  description = "MinIO private IP in Data Subnet"
  value       = aws_instance.minio.private_ip
}

output "endpoint" {
  description = "MinIO internal endpoint (http://<private-ip>:9000)"
  value       = "${aws_instance.minio.private_ip}:9000"
}

output "bucket_name" {
  description = "Primary evidence bucket name"
  value       = var.minio_bucket
}
