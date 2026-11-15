#!/bin/sh
set -eu

ACCOUNT=000000000000
REGION="${AWS_DEFAULT_REGION:-sa-east-1}"

create_fifo_queue() {
  awslocal sqs create-queue --queue-name "$1-dlq.fifo" \
    --attributes FifoQueue=true,ContentBasedDeduplication=false >/dev/null
  dlq_arn="arn:aws:sqs:${REGION}:${ACCOUNT}:$1-dlq.fifo"
  awslocal sqs create-queue --queue-name "$1.fifo" \
    --attributes "{\"FifoQueue\":\"true\",\"ContentBasedDeduplication\":\"false\",\"RedrivePolicy\":\"{\\\"deadLetterTargetArn\\\":\\\"${dlq_arn}\\\",\\\"maxReceiveCount\\\":\\\"5\\\"}\"}" >/dev/null
}

create_fifo_queue os-inbox
create_fifo_queue billing-inbox
create_fifo_queue execution-inbox

for topic in billing-events execution-events; do
  topic_arn=$(awslocal sns create-topic --name "${topic}.fifo" \
    --attributes FifoTopic=true,ContentBasedDeduplication=false --query TopicArn --output text)
  awslocal sns subscribe --topic-arn "$topic_arn" --protocol sqs \
    --notification-endpoint "arn:aws:sqs:${REGION}:${ACCOUNT}:os-inbox.fifo" \
    --attributes RawMessageDelivery=true >/dev/null
done

touch /tmp/os-service-ready
