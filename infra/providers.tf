provider "aws" {
  region = var.region

  default_tags {
    tags = {
      Project     = "oficina-mecanica"
      Service     = local.service
      Environment = var.environment
      ManagedBy   = "terraform"
      Repository  = "SOAT-15-Oficina/oficina-mecanica-os-service"
    }
  }
}
