locals {
  service    = "os-service"
  name       = "oficina-${var.environment}-os"
  ssm_prefix = "/oficina-mecanica/${var.environment}"
}

data "aws_caller_identity" "current" {}

data "aws_ssm_parameter" "platform" {
  for_each = toset([
    "vpc_id",
    "private_subnet_ids",
    "kube_namespace",
    "eks_oidc_provider_arn",
    "eks_node_security_group_id",
    "alb_listener_arn",
    "billing_inbox_queue_arn",
    "billing_events_topic_arn",
    "execution_inbox_queue_arn",
    "execution_events_topic_arn",
  ])

  name = "${local.ssm_prefix}/${each.key}"
}

locals {
  vpc_id              = data.aws_ssm_parameter.platform["vpc_id"].value
  private_subnet_ids  = split(",", data.aws_ssm_parameter.platform["private_subnet_ids"].value)
  kube_namespace      = data.aws_ssm_parameter.platform["kube_namespace"].value
  oidc_provider_arn   = data.aws_ssm_parameter.platform["eks_oidc_provider_arn"].value
  oidc_provider_host  = element(split("oidc-provider/", local.oidc_provider_arn), 1)
  node_security_group = data.aws_ssm_parameter.platform["eks_node_security_group_id"].value
  alb_listener_arn    = data.aws_ssm_parameter.platform["alb_listener_arn"].value
  billing_inbox_arn   = data.aws_ssm_parameter.platform["billing_inbox_queue_arn"].value
  execution_inbox_arn = data.aws_ssm_parameter.platform["execution_inbox_queue_arn"].value
  event_topic_arns = {
    billing   = nonsensitive(data.aws_ssm_parameter.platform["billing_events_topic_arn"].value)
    execution = nonsensitive(data.aws_ssm_parameter.platform["execution_events_topic_arn"].value)
  }
}
