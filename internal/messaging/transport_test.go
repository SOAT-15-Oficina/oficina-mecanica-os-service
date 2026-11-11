package messaging

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sns"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeSQS struct {
	mu         sync.Mutex
	sent       []*sqs.SendMessageInput
	deleted    []string
	inbox      []sqstypes.Message
	sendErr    error
	receiveErr error
	deleteErr  error
	receives   int
}

func (f *fakeSQS) SendMessage(_ context.Context, in *sqs.SendMessageInput, _ ...func(*sqs.Options)) (*sqs.SendMessageOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.sendErr != nil {
		return nil, f.sendErr
	}
	f.sent = append(f.sent, in)
	return &sqs.SendMessageOutput{}, nil
}

func (f *fakeSQS) ReceiveMessage(_ context.Context, in *sqs.ReceiveMessageInput, _ ...func(*sqs.Options)) (*sqs.ReceiveMessageOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.receives++
	if f.receiveErr != nil {
		return nil, f.receiveErr
	}
	msgs := f.inbox
	f.inbox = nil
	return &sqs.ReceiveMessageOutput{Messages: msgs}, nil
}

func (f *fakeSQS) DeleteMessage(_ context.Context, in *sqs.DeleteMessageInput, _ ...func(*sqs.Options)) (*sqs.DeleteMessageOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.deleteErr != nil {
		return nil, f.deleteErr
	}
	f.deleted = append(f.deleted, aws.ToString(in.ReceiptHandle))
	return &sqs.DeleteMessageOutput{}, nil
}

type fakeSNS struct {
	published []*sns.PublishInput
	err       error
}

func (f *fakeSNS) Publish(_ context.Context, in *sns.PublishInput, _ ...func(*sns.Options)) (*sns.PublishOutput, error) {
	if f.err != nil {
		return nil, f.err
	}
	f.published = append(f.published, in)
	return &sns.PublishOutput{}, nil
}

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)) }

func TestPublishToQueueUsesFIFOAttributes(t *testing.T) {
	q, s := &fakeSQS{}, &fakeSNS{}
	env := validEnvelope()

	err := NewAWSPublisher(q, s).Publish(context.Background(), "http://localstack:4566/000/billing-inbox.fifo", env, Carrier{"traceparent": "00-abc"})
	require.NoError(t, err)

	require.Len(t, q.sent, 1)
	assert.Empty(t, s.published)
	in := q.sent[0]
	assert.Equal(t, env.WorkOrderID.String(), aws.ToString(in.MessageGroupId))
	assert.Equal(t, env.MessageID.String(), aws.ToString(in.MessageDeduplicationId))
	assert.Equal(t, env.Type, aws.ToString(in.MessageAttributes[AttributeType].StringValue))
	assert.Equal(t, "00-abc", aws.ToString(in.MessageAttributes["traceparent"].StringValue))

	decoded, err := Decode([]byte(aws.ToString(in.MessageBody)))
	require.NoError(t, err)
	assert.Equal(t, env.MessageID, decoded.MessageID)
}

func TestPublishToTopicUsesSNS(t *testing.T) {
	q, s := &fakeSQS{}, &fakeSNS{}
	env := validEnvelope()

	err := NewAWSPublisher(q, s).Publish(context.Background(), "arn:aws:sns:sa-east-1:000:billing-events.fifo", env, Carrier{"x-datadog-trace-id": "1"})
	require.NoError(t, err)

	require.Len(t, s.published, 1)
	assert.Empty(t, q.sent)
	in := s.published[0]
	assert.Equal(t, env.WorkOrderID.String(), aws.ToString(in.MessageGroupId))
	assert.Equal(t, env.MessageID.String(), aws.ToString(in.MessageDeduplicationId))
	assert.Equal(t, "1", aws.ToString(in.MessageAttributes["x-datadog-trace-id"].StringValue))
}

func TestPublishPropagatesErrors(t *testing.T) {
	boom := errors.New("boom")
	env := validEnvelope()

	err := NewAWSPublisher(&fakeSQS{sendErr: boom}, &fakeSNS{}).Publish(context.Background(), "queue", env, nil)
	assert.ErrorIs(t, err, boom)

	err = NewAWSPublisher(&fakeSQS{}, &fakeSNS{err: boom}).Publish(context.Background(), "arn:aws:sns:x", env, nil)
	assert.ErrorIs(t, err, boom)
}

func sqsMessage(t *testing.T, env Envelope, receipt string) sqstypes.Message {
	t.Helper()
	body, err := json.Marshal(env)
	require.NoError(t, err)
	return sqstypes.Message{
		Body:          aws.String(string(body)),
		ReceiptHandle: aws.String(receipt),
		MessageAttributes: map[string]sqstypes.MessageAttributeValue{
			AttributeType: {StringValue: aws.String(env.Type)},
			"traceparent": {StringValue: aws.String("00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")},
		},
	}
}

func TestConsumerDeletesHandledMessages(t *testing.T) {
	first, second := validEnvelope(), validEnvelope()
	q := &fakeSQS{inbox: []sqstypes.Message{sqsMessage(t, first, "r1"), sqsMessage(t, second, "r2")}}

	var handled []Envelope
	c := NewConsumer(q, "queue", 30*time.Second, func(_ context.Context, env Envelope) error {
		handled = append(handled, env)
		return nil
	}, discardLogger())

	require.NoError(t, c.Poll(context.Background()))
	require.Len(t, handled, 2)
	assert.Equal(t, first.MessageID, handled[0].MessageID)
	assert.Equal(t, []string{"r1", "r2"}, q.deleted)
}

func TestConsumerStopsBatchOnHandlerFailure(t *testing.T) {
	q := &fakeSQS{inbox: []sqstypes.Message{sqsMessage(t, validEnvelope(), "r1"), sqsMessage(t, validEnvelope(), "r2")}}

	calls := 0
	c := NewConsumer(q, "queue", time.Second, func(context.Context, Envelope) error {
		calls++
		return errors.New("db down")
	}, discardLogger())

	require.NoError(t, c.Poll(context.Background()))
	assert.Equal(t, 1, calls)
	assert.Empty(t, q.deleted)
}

func TestConsumerLeavesInvalidEnvelopeForDLQ(t *testing.T) {
	q := &fakeSQS{inbox: []sqstypes.Message{{Body: aws.String("{}"), ReceiptHandle: aws.String("r1")}}}
	c := NewConsumer(q, "queue", time.Second, func(context.Context, Envelope) error {
		t.Fatal("handler must not run")
		return nil
	}, discardLogger())

	require.NoError(t, c.Poll(context.Background()))
	assert.Empty(t, q.deleted)
}

func TestConsumerReportsDeleteFailure(t *testing.T) {
	q := &fakeSQS{inbox: []sqstypes.Message{sqsMessage(t, validEnvelope(), "r1")}, deleteErr: errors.New("gone")}
	c := NewConsumer(q, "queue", time.Second, func(context.Context, Envelope) error { return nil }, discardLogger())
	require.NoError(t, c.Poll(context.Background()))
	assert.Empty(t, q.deleted)
}

func TestConsumerPollReturnsReceiveError(t *testing.T) {
	q := &fakeSQS{receiveErr: errors.New("throttled")}
	c := NewConsumer(q, "queue", time.Minute, nil, discardLogger())
	assert.Error(t, c.Poll(context.Background()))
}

func TestConsumerRunStopsWithContext(t *testing.T) {
	q := &fakeSQS{receiveErr: errors.New("throttled")}
	c := NewConsumer(q, "queue", time.Second, nil, discardLogger())

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	done := make(chan struct{})
	go func() { c.Run(ctx); close(done) }()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("consumer did not stop")
	}
	assert.GreaterOrEqual(t, q.receives, 1)
}
