package messaging

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sns"
	snstypes "github.com/aws/aws-sdk-go-v2/service/sns/types"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"
)

const AttributeType = "message_type"

type SQSAPI interface {
	SendMessage(ctx context.Context, in *sqs.SendMessageInput, opts ...func(*sqs.Options)) (*sqs.SendMessageOutput, error)
	ReceiveMessage(ctx context.Context, in *sqs.ReceiveMessageInput, opts ...func(*sqs.Options)) (*sqs.ReceiveMessageOutput, error)
	DeleteMessage(ctx context.Context, in *sqs.DeleteMessageInput, opts ...func(*sqs.Options)) (*sqs.DeleteMessageOutput, error)
}

type SNSAPI interface {
	Publish(ctx context.Context, in *sns.PublishInput, opts ...func(*sns.Options)) (*sns.PublishOutput, error)
}

type Publisher interface {
	Publish(ctx context.Context, destination string, env Envelope, carrier Carrier) error
}

type AWSPublisher struct {
	sqs SQSAPI
	sns SNSAPI
}

func NewAWSPublisher(sqsClient SQSAPI, snsClient SNSAPI) *AWSPublisher {
	return &AWSPublisher{sqs: sqsClient, sns: snsClient}
}

func (p *AWSPublisher) Publish(ctx context.Context, destination string, env Envelope, carrier Carrier) error {
	body, err := json.Marshal(env)
	if err != nil {
		return fmt.Errorf("marshal envelope: %w", err)
	}

	if strings.HasPrefix(destination, "arn:aws:sns:") {
		attrs := map[string]snstypes.MessageAttributeValue{
			AttributeType: {DataType: aws.String("String"), StringValue: aws.String(env.Type)},
		}
		for k, v := range carrier {
			attrs[k] = snstypes.MessageAttributeValue{DataType: aws.String("String"), StringValue: aws.String(v)}
		}
		_, err = p.sns.Publish(ctx, &sns.PublishInput{
			TopicArn:               aws.String(destination),
			Message:                aws.String(string(body)),
			MessageGroupId:         aws.String(env.GroupID()),
			MessageDeduplicationId: aws.String(env.MessageID.String()),
			MessageAttributes:      attrs,
		})
		if err != nil {
			return fmt.Errorf("sns publish %s: %w", env.Type, err)
		}
		return nil
	}

	attrs := map[string]sqstypes.MessageAttributeValue{
		AttributeType: {DataType: aws.String("String"), StringValue: aws.String(env.Type)},
	}
	for k, v := range carrier {
		attrs[k] = sqstypes.MessageAttributeValue{DataType: aws.String("String"), StringValue: aws.String(v)}
	}
	_, err = p.sqs.SendMessage(ctx, &sqs.SendMessageInput{
		QueueUrl:               aws.String(destination),
		MessageBody:            aws.String(string(body)),
		MessageGroupId:         aws.String(env.GroupID()),
		MessageDeduplicationId: aws.String(env.MessageID.String()),
		MessageAttributes:      attrs,
	})
	if err != nil {
		return fmt.Errorf("sqs send %s: %w", env.Type, err)
	}
	return nil
}

type Handler func(ctx context.Context, env Envelope) error

type Consumer struct {
	sqs      SQSAPI
	queueURL string
	wait     time.Duration
	handler  Handler
	logger   *slog.Logger
}

func NewConsumer(client SQSAPI, queueURL string, wait time.Duration, handler Handler, logger *slog.Logger) *Consumer {
	return &Consumer{sqs: client, queueURL: queueURL, wait: wait, handler: handler, logger: logger}
}

func (c *Consumer) Run(ctx context.Context) {
	for ctx.Err() == nil {
		if err := c.Poll(ctx); err != nil && ctx.Err() == nil {
			c.logger.ErrorContext(ctx, "messaging: receive failed", slog.String("error", err.Error()))
			sleep(ctx, time.Second)
		}
	}
}

func (c *Consumer) Poll(ctx context.Context) error {
	waitSeconds := int32(c.wait / time.Second)
	if waitSeconds > 20 {
		waitSeconds = 20
	}

	out, err := c.sqs.ReceiveMessage(ctx, &sqs.ReceiveMessageInput{
		QueueUrl:              aws.String(c.queueURL),
		MaxNumberOfMessages:   10,
		WaitTimeSeconds:       waitSeconds,
		MessageAttributeNames: []string{"All"},
	})
	if err != nil {
		return fmt.Errorf("sqs receive: %w", err)
	}

	for _, msg := range out.Messages {
		if !c.handle(ctx, msg) {
			return nil
		}
	}
	return nil
}

func (c *Consumer) handle(ctx context.Context, msg sqstypes.Message) bool {
	env, err := Decode([]byte(aws.ToString(msg.Body)))
	if err != nil {
		c.logger.ErrorContext(ctx, "messaging: invalid envelope, leaving for the DLQ",
			slog.String("error", err.Error()), slog.String("sqs_message_id", aws.ToString(msg.MessageId)))
		return false
	}

	carrier := Carrier{}
	for k, v := range msg.MessageAttributes {
		if k != AttributeType && v.StringValue != nil {
			carrier[k] = *v.StringValue
		}
	}

	spanCtx, finish := StartConsumeSpan(ctx, env, carrier)
	logger := c.logger.With(
		slog.String("message_id", env.MessageID.String()),
		slog.String("message_type", env.Type),
		slog.String("saga_id", env.SagaID.String()),
		slog.String("work_order_id", env.WorkOrderID.String()),
		slog.String("request_id", env.CorrelationID),
	)

	err = c.handler(spanCtx, env)
	finish(err)
	if err != nil {
		logger.ErrorContext(spanCtx, "messaging: handler failed, message will be retried", slog.String("error", err.Error()))
		return false
	}

	if _, err := c.sqs.DeleteMessage(ctx, &sqs.DeleteMessageInput{
		QueueUrl:      aws.String(c.queueURL),
		ReceiptHandle: msg.ReceiptHandle,
	}); err != nil {
		logger.ErrorContext(spanCtx, "messaging: delete failed", slog.String("error", err.Error()))
		return false
	}

	logger.InfoContext(spanCtx, "messaging: message consumed", slog.String("event", "message.consumed"))
	return true
}

func sleep(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}
