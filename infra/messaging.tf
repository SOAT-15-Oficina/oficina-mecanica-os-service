resource "aws_sqs_queue" "inbox_dlq" {
  name                      = "${local.name}-inbox-dlq.fifo"
  fifo_queue                = true
  message_retention_seconds = 1209600
  sqs_managed_sse_enabled   = true
}

resource "aws_sqs_queue" "inbox" {
  name                        = "${local.name}-inbox.fifo"
  fifo_queue                  = true
  content_based_deduplication = false
  deduplication_scope         = "messageGroup"
  fifo_throughput_limit       = "perMessageGroupId"
  visibility_timeout_seconds  = 60
  receive_wait_time_seconds   = 20
  sqs_managed_sse_enabled     = true

  redrive_policy = jsonencode({
    deadLetterTargetArn = aws_sqs_queue.inbox_dlq.arn
    maxReceiveCount     = 5
  })
}

data "aws_iam_policy_document" "inbox" {
  statement {
    sid       = "AllowEventTopics"
    actions   = ["sqs:SendMessage"]
    resources = [aws_sqs_queue.inbox.arn]

    principals {
      type        = "Service"
      identifiers = ["sns.amazonaws.com"]
    }

    condition {
      test     = "ArnEquals"
      variable = "aws:SourceArn"
      values   = local.event_topic_arns
    }
  }
}

resource "aws_sqs_queue_policy" "inbox" {
  queue_url = aws_sqs_queue.inbox.id
  policy    = data.aws_iam_policy_document.inbox.json
}

resource "aws_sns_topic_subscription" "events" {
  for_each = toset(local.event_topic_arns)

  topic_arn            = each.value
  protocol             = "sqs"
  endpoint             = aws_sqs_queue.inbox.arn
  raw_message_delivery = true

  depends_on = [aws_sqs_queue_policy.inbox]
}
