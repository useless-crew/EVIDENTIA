# Evidentia AWS Infrastructure — Terraform
#
# Modular Terraform configuration for deploying the Evidentia platform
# to AWS. See docs/aws-deployment.md for the full deployment guide.
#
# Structure:
#   environments/demo/  — SIH demo deployment (affordable, single-region)
#   modules/            — reusable infrastructure modules
#
# Usage:
#   cd environments/demo
#   cp terraform.tfvars.example terraform.tfvars  # edit with real values
#   terraform init
#   terraform plan
#   terraform apply  # ONLY after reviewing the plan
#
# NEVER store terraform.tfvars or *.tfstate in Git.
