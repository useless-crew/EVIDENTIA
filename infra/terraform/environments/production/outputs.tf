# Evidentia — Production Terraform Outputs

output "alb_dns_name" {
  description = "Application Load Balancer DNS Name"
  value       = module.ecs.alb_dns_name
}

output "api_url" {
  description = "Target API Endpoint URL"
  value       = "https://${var.api_subdomain}.${var.domain_name}"
}

output "cloudfront_domain" {
  description = "CloudFront CDN domain name"
  value       = module.storage.cloudfront_domain_name
}

output "cloudfront_distribution_id" {
  description = "CloudFront distribution ID (used for CI/CD cache invalidation)"
  value       = module.storage.cloudfront_distribution_id
}

output "frontend_url" {
  description = "Target Frontend Application URL"
  value       = "https://${var.frontend_subdomain}.${var.domain_name}"
}

output "frontend_s3_bucket" {
  description = "S3 bucket name for hosting frontend static files"
  value       = module.storage.frontend_bucket_name
}

output "ecr_api_url" {
  description = "ECR Repository URL for the API image"
  value       = module.ecr.api_repository_url
}

output "ecr_worker_url" {
  description = "ECR Repository URL for the Worker image"
  value       = module.ecr.worker_repository_url
}

output "rds_endpoint" {
  description = "Amazon RDS PostgreSQL primary endpoint"
  value       = module.rds.endpoint
}

output "redis_endpoint" {
  description = "Amazon ElastiCache Redis primary endpoint"
  value       = module.redis.primary_endpoint
}

output "minio_private_ip" {
  description = "MinIO private EC2 IP address"
  value       = module.minio_ec2.private_ip
}

output "minio_endpoint" {
  description = "MinIO internal S3 API endpoint"
  value       = module.minio_ec2.endpoint
}

output "fabric_private_ip" {
  description = "Hyperledger Fabric private EC2 IP address"
  value       = module.fabric_ec2.private_ip
}

output "fabric_peer_endpoint" {
  description = "Hyperledger Fabric Peer gRPC endpoint"
  value       = module.fabric_ec2.peer_endpoint
}
