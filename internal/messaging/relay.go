package messaging

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"
)

type OutboxMessage struct {
	ID          uuid.UUID
	Destination string
	Envelope    Envelope
	Carrier     Carrier
}

type OutboxStore interface {
	FetchPending(ctx context.Context, limit int) ([]OutboxMessage, error)
	MarkPublished(ctx context.Context, id uuid.UUID) error
}

type Relay struct {
	store     OutboxStore
	publisher Publisher
	interval  time.Duration
	batch     int
	logger    *slog.Logger
}

func NewRelay(store OutboxStore, publisher Publisher, interval time.Duration, logger *slog.Logger) *Relay {
	return &Relay{store: store, publisher: publisher, interval: interval, batch: 50, logger: logger}
}

func (r *Relay) Run(ctx context.Context) {
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	for {
		if _, err := r.Flush(ctx); err != nil && ctx.Err() == nil {
			r.logger.ErrorContext(ctx, "outbox: flush failed", slog.String("error", err.Error()))
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (r *Relay) Flush(ctx context.Context) (int, error) {
	pending, err := r.store.FetchPending(ctx, r.batch)
	if err != nil {
		return 0, err
	}

	published := 0
	for _, msg := range pending {
		spanCtx, finish := StartPublishSpan(ctx, msg.Envelope, msg.Destination, msg.Carrier)
		carrier := InjectTrace(spanCtx)
		if len(carrier) == 0 {
			carrier = msg.Carrier
		}
		err := r.publisher.Publish(spanCtx, msg.Destination, msg.Envelope, carrier)
		finish(err)
		if err != nil {
			return published, err
		}
		if err := r.store.MarkPublished(ctx, msg.ID); err != nil {
			return published, err
		}
		r.logger.InfoContext(spanCtx, "outbox: message published",
			slog.String("event", "message.published"),
			slog.String("message_id", msg.Envelope.MessageID.String()),
			slog.String("message_type", msg.Envelope.Type),
			slog.String("saga_id", msg.Envelope.SagaID.String()),
			slog.String("work_order_id", msg.Envelope.WorkOrderID.String()),
		)
		published++
	}
	return published, nil
}
