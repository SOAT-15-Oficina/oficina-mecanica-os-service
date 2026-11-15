locals {
  published = {
    os_inbox_queue_url   = aws_sqs_queue.inbox.url
    os_inbox_queue_arn   = aws_sqs_queue.inbox.arn
    os_inbox_dlq_arn     = aws_sqs_queue.inbox_dlq.arn
    os_ecr_repository    = aws_ecr_repository.service.repository_url
    os_database_endpoint = aws_db_instance.os.address
    os_database_name     = aws_db_instance.os.db_name
    os_database_user     = aws_db_instance.os.username
    os_secret_arn        = aws_secretsmanager_secret.os.arn
    os_role_arn          = aws_iam_role.service.arn
    os_target_group_arn  = aws_lb_target_group.service.arn
    os_database_sg_id    = aws_security_group.db.id
  }
}

resource "aws_ssm_parameter" "published" {
  for_each = local.published

  name      = "${local.ssm_prefix}/${each.key}"
  type      = "String"
  value     = each.value
  overwrite = true
}

output "published" {
  value = local.published
}
