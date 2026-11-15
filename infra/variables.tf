variable "environment" {
  type = string

  validation {
    condition     = contains(["homolog", "prod"], var.environment)
    error_message = "environment deve ser homolog ou prod."
  }
}

variable "region" {
  type    = string
  default = "sa-east-1"
}

variable "db_instance_class" {
  type    = string
  default = "db.t4g.micro"
}

variable "db_allocated_storage" {
  type    = number
  default = 20
}

variable "path_pattern" {
  type    = string
  default = "/*"
}

variable "listener_rule_priority" {
  type    = number
  default = 100
}
