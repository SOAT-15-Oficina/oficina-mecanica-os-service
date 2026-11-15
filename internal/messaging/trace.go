package messaging

import (
	"context"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)

type Carrier map[string]string

func InjectTrace(ctx context.Context) Carrier {
	carrier := Carrier{}
	span, ok := tracer.SpanFromContext(ctx)
	if !ok || span == nil {
		return carrier
	}
	_ = tracer.Inject(span.Context(), tracer.TextMapCarrier(carrier))
	return carrier
}

func StartConsumeSpan(ctx context.Context, env Envelope, carrier Carrier) (context.Context, func(error)) {
	opts := []tracer.StartSpanOption{
		tracer.SpanType("queue"),
		tracer.ResourceName(env.Type),
		tracer.Tag("messaging.message_id", env.MessageID.String()),
		tracer.Tag("saga_id", env.SagaID.String()),
		tracer.Tag("work_order_id", env.WorkOrderID.String()),
	}
	if len(carrier) > 0 {
		if parent, err := tracer.Extract(tracer.TextMapCarrier(carrier)); err == nil && parent != nil {
			opts = append(opts, tracer.ChildOf(parent))
		}
	}

	span, ctx := tracer.StartSpanFromContext(ctx, "messaging.consume", opts...)
	return ctx, func(err error) { span.Finish(tracer.WithError(err)) }
}

func StartPublishSpan(ctx context.Context, env Envelope, destination string, parent Carrier) (context.Context, func(error)) {
	opts := []tracer.StartSpanOption{
		tracer.SpanType("queue"),
		tracer.ResourceName(env.Type),
		tracer.Tag("messaging.destination", destination),
		tracer.Tag("messaging.message_id", env.MessageID.String()),
		tracer.Tag("saga_id", env.SagaID.String()),
		tracer.Tag("work_order_id", env.WorkOrderID.String()),
	}
	if len(parent) > 0 {
		if sc, err := tracer.Extract(tracer.TextMapCarrier(parent)); err == nil && sc != nil {
			opts = append(opts, tracer.ChildOf(sc))
		}
	}

	span, ctx := tracer.StartSpanFromContext(ctx, "messaging.publish", opts...)
	return ctx, func(err error) { span.Finish(tracer.WithError(err)) }
}
