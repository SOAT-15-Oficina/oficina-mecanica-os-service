package messaging

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type memoryOutbox struct {
	mu        sync.Mutex
	pending   []OutboxMessage
	published []uuid.UUID
	fetchErr  error
	markErr   error
}

func (m *memoryOutbox) FetchPending(_ context.Context, limit int) ([]OutboxMessage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.fetchErr != nil {
		return nil, m.fetchErr
	}
	var out []OutboxMessage
	for _, msg := range m.pending {
		if !contains(m.published, msg.ID) && len(out) < limit {
			out = append(out, msg)
		}
	}
	return out, nil
}

func (m *memoryOutbox) MarkPublished(_ context.Context, id uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.markErr != nil {
		return m.markErr
	}
	m.published = append(m.published, id)
	return nil
}

func contains(ids []uuid.UUID, id uuid.UUID) bool {
	for _, x := range ids {
		if x == id {
			return true
		}
	}
	return false
}

type recordingPublisher struct {
	mu       sync.Mutex
	sent     []Envelope
	carriers []Carrier
	failOn   int
}

func (p *recordingPublisher) Publish(_ context.Context, _ string, env Envelope, carrier Carrier) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.failOn > 0 && len(p.sent)+1 == p.failOn {
		return errors.New("broker down")
	}
	p.sent = append(p.sent, env)
	p.carriers = append(p.carriers, carrier)
	return nil
}

func outboxMessage() OutboxMessage {
	env := validEnvelope()
	return OutboxMessage{ID: env.MessageID, Destination: "queue", Envelope: env, Carrier: Carrier{"traceparent": "x"}}
}

func TestRelayPublishesInOrderAndMarks(t *testing.T) {
	a, b := outboxMessage(), outboxMessage()
	store := &memoryOutbox{pending: []OutboxMessage{a, b}}
	pub := &recordingPublisher{}

	n, err := NewRelay(store, pub, time.Second, discardLogger()).Flush(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 2, n)
	assert.Equal(t, []uuid.UUID{a.ID, b.ID}, store.published)
	assert.Equal(t, a.Envelope.MessageID, pub.sent[0].MessageID)
	assert.Equal(t, Carrier{"traceparent": "x"}, pub.carriers[0])
}

func TestRelayStopsAtFirstFailureToKeepOrder(t *testing.T) {
	a, b := outboxMessage(), outboxMessage()
	store := &memoryOutbox{pending: []OutboxMessage{a, b}}
	pub := &recordingPublisher{failOn: 1}

	n, err := NewRelay(store, pub, time.Second, discardLogger()).Flush(context.Background())
	assert.Error(t, err)
	assert.Equal(t, 0, n)
	assert.Empty(t, store.published)
}

func TestRelayPropagatesStoreErrors(t *testing.T) {
	_, err := NewRelay(&memoryOutbox{fetchErr: errors.New("db")}, &recordingPublisher{}, time.Second, discardLogger()).Flush(context.Background())
	assert.Error(t, err)

	store := &memoryOutbox{pending: []OutboxMessage{outboxMessage()}, markErr: errors.New("db")}
	_, err = NewRelay(store, &recordingPublisher{}, time.Second, discardLogger()).Flush(context.Background())
	assert.Error(t, err)
}

func TestRelayRunFlushesUntilCanceled(t *testing.T) {
	store := &memoryOutbox{pending: []OutboxMessage{outboxMessage()}}
	pub := &recordingPublisher{}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { NewRelay(store, pub, 10*time.Millisecond, discardLogger()).Run(ctx); close(done) }()

	require.Eventually(t, func() bool {
		store.mu.Lock()
		defer store.mu.Unlock()
		return len(store.published) == 1
	}, 2*time.Second, 10*time.Millisecond)
	cancel()
	<-done

	failing := &memoryOutbox{fetchErr: errors.New("db")}
	ctx, cancel = context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	NewRelay(failing, pub, 10*time.Millisecond, discardLogger()).Run(ctx)
}

func TestTraceHelpersWorkWithoutTracer(t *testing.T) {
	env := validEnvelope()
	assert.Empty(t, InjectTrace(context.Background()))

	ctx, finish := StartConsumeSpan(context.Background(), env, Carrier{"x-datadog-trace-id": "1", "x-datadog-parent-id": "2"})
	assert.NotNil(t, ctx)
	finish(nil)

	ctx, finish = StartPublishSpan(context.Background(), env, "queue", Carrier{"x-datadog-trace-id": "1", "x-datadog-parent-id": "2"})
	assert.NotNil(t, ctx)
	finish(errors.New("x"))
}

func TestNewSagaStartHasNoCausation(t *testing.T) {
	env, err := NewSagaStart(TypeScheduleDiagnosis, uuid.New(), uuid.New(), "req", ScheduleDiagnosis{Branch: "MATRIZ"})
	require.NoError(t, err)
	assert.Nil(t, env.CausationID)
	require.NoError(t, env.Validate())
}
