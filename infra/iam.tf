data "aws_iam_policy_document" "assume" {
  statement {
    actions = ["sts:AssumeRoleWithWebIdentity"]

    principals {
      type        = "Federated"
      identifiers = [local.oidc_provider_arn]
    }

    condition {
      test     = "StringEquals"
      variable = "${local.oidc_provider_host}:sub"
      values   = ["system:serviceaccount:${local.kube_namespace}:${local.service}"]
    }

    condition {
      test     = "StringEquals"
      variable = "${local.oidc_provider_host}:aud"
      values   = ["sts.amazonaws.com"]
    }
  }
}

resource "aws_iam_role" "service" {
  name                 = local.name
  assume_role_policy   = data.aws_iam_policy_document.assume.json
  permissions_boundary = "arn:aws:iam::${data.aws_caller_identity.current.account_id}:policy/oficina-mecanica-${var.environment}-service-boundary"
}

data "aws_iam_policy_document" "service" {
  statement {
    sid       = "ConsumeInbox"
    actions   = ["sqs:ReceiveMessage", "sqs:DeleteMessage", "sqs:ChangeMessageVisibility", "sqs:GetQueueAttributes"]
    resources = [aws_sqs_queue.inbox.arn]
  }

  statement {
    sid       = "SendCommands"
    actions   = ["sqs:SendMessage", "sqs:GetQueueAttributes"]
    resources = [local.billing_inbox_arn, local.execution_inbox_arn]
  }

  statement {
    sid       = "SendEmail"
    actions   = ["ses:SendEmail", "ses:SendRawEmail"]
    resources = ["*"]
  }
}

resource "aws_iam_role_policy" "service" {
  name   = "os-service"
  role   = aws_iam_role.service.id
  policy = data.aws_iam_policy_document.service.json
}
