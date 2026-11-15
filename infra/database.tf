resource "random_password" "db" {
  length  = 32
  special = false
}

resource "aws_db_subnet_group" "os" {
  name       = local.name
  subnet_ids = local.private_subnet_ids
}

resource "aws_security_group" "db" {
  name        = "${local.name}-db"
  description = "PostgreSQL do os-service"
  vpc_id      = local.vpc_id
}

resource "aws_vpc_security_group_ingress_rule" "db_from_nodes" {
  security_group_id            = aws_security_group.db.id
  referenced_security_group_id = local.node_security_group
  ip_protocol                  = "tcp"
  from_port                    = 5432
  to_port                      = 5432
}

resource "aws_db_instance" "os" {
  identifier              = local.name
  engine                  = "postgres"
  engine_version          = "17"
  instance_class          = var.db_instance_class
  allocated_storage       = var.db_allocated_storage
  storage_encrypted       = true
  db_name                 = "workshop"
  username                = "workshop"
  password                = random_password.db.result
  db_subnet_group_name    = aws_db_subnet_group.os.name
  vpc_security_group_ids  = [aws_security_group.db.id]
  publicly_accessible     = false
  skip_final_snapshot     = true
  deletion_protection     = false
  backup_retention_period = 1
}

resource "aws_secretsmanager_secret" "os" {
  name                    = "oficina-mecanica/${var.environment}/os-service"
  recovery_window_in_days = 0
}

resource "aws_secretsmanager_secret_version" "os" {
  secret_id = aws_secretsmanager_secret.os.id
  secret_string = jsonencode({
    DATABASE_PASSWORD = random_password.db.result
  })
}
