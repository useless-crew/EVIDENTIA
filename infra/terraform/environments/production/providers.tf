# Evidentia — Production Terraform Providers

terraform {
  required_version = ">= 1.5"

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.0"
    }
  }

  # For production, configure remote state with S3 and DynamoDB locking:
  # backend "s3" {
  #   bucket         = "evidentia-prod-terraform-state"
  #   key            = "production/terraform.tfstate"
  #   region         = "ap-south-1"
  #   dynamodb_table = "evidentia-prod-terraform-locks"
  #   encrypt        = true
  # }
}

provider "aws" {
  region = var.aws_region

  default_tags {
    tags = {
      Project     = "evidentia"
      Environment = "production"
      ManagedBy   = "terraform"
      Security    = "defense-in-depth"
    }
  }
}

# CloudFront ACM certificates must reside in us-east-1
provider "aws" {
  alias  = "us_east_1"
  region = "us-east-1"

  default_tags {
    tags = {
      Project     = "evidentia"
      Environment = "production"
      ManagedBy   = "terraform"
    }
  }
}
