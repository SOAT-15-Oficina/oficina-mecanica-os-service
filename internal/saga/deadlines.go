package saga

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/SOAT-15-Oficina/oficina-mecanica-os-service/internal/domain"
	"github.com/SOAT-15-Oficina/oficina-mecanica-os-service/internal/messaging"
	"github.com/SOAT-15-Oficina/oficina-mecanica-os-service/internal/messaging/pgoutbox"
	"github.com/SOAT-15-Oficina/oficina-mecanica-os-service/internal/observability"
	"github.com/jackc/pgx/v5"
)

func (o *Orchestrator) RunDeadlines(ctx context.Context) {
	tick := o.cfg.Tick
	if tick <= 0 {
		tick = time.Second
	}
	ticker := time.NewTicker(tick)
	defer ticker.Stop()
	for {
		if _, err := o.Tick(ctx); err != nil && ctx.Err() == nil {
			observability.FromContext(ctx).LogAttrs(ctx, slog.LevelError, "saga deadline sweep failed",
				observability.Integration(observability.IntegrationRDS), observability.Err(err))
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (o *Orchestrator) Tick(ctx context.Context) (int, error) {
	processed := 0
	err := o.inTx(ctx, func(tx pgx.Tx, eff *effects) error {
		processed = 0
		due, err := dueSagas(ctx, tx, o.now(), o.cfg.TickBatch)
		if err != nil {
			return err
		}
		for _, inst := range due {
			r := o.newRun(tx, inst, causeOf(inst), CauseDeadline, eff)
			if err := o.expire(ctx, r); err != nil {
				return fmt.Errorf("expire saga %s: %w", inst.ID, err)
			}
			if err := saveSaga(ctx, tx, inst, r.now); err != nil {
				return err
			}
			processed++
		}
		return nil
	})
	return processed, err
}

func causeOf(inst *domain.SagaInstance) messaging.Envelope {
	cause := messaging.Envelope{SagaID: inst.ID, WorkOrderID: inst.WorkOrderID}
	if inst.LastCommand != nil {
		var last messaging.Envelope
		if json.Unmarshal(inst.LastCommand.Envelope, &last) == nil {
			cause.CorrelationID = last.CorrelationID
		}
	}
	return cause
}

func (o *Orchestrator) expire(ctx context.Context, r *run) error {
	inst := r.inst
	if inst.Status == domain.SagaStarted {
		switch inst.CurrentStep {
		case StepAwaitApproval:
			if err := rejectPendingItems(ctx, r.tx, inst.WorkOrderID, r.now); err != nil {
				return err
			}
			return startCompensation(ctx, r, messaging.ReasonDeadline, false)
		case StepAwaitPayment:
			return startCompensation(ctx, r, messaging.ReasonExpired, false)
		}
	}

	if inst.LastCommand == nil {
		inst.DeadlineAt = nil
		return nil
	}
	if inst.Attempts < o.cfg.MaxAttempts {
		return o.retry(ctx, r)
	}
	if inst.Status == domain.SagaStarted {
		return startCompensation(ctx, r, ReasonNoResponse, false)
	}

	reason := ReasonNoResponse
	inst.Status = domain.SagaFailed
	inst.FailureReason = &reason
	inst.DeadlineAt = nil
	r.log(slog.LevelError, "saga failed while compensating", observability.EventSagaFailed,
		slog.Any("pending_compensations", inst.PendingCompensations))
	return nil
}

func (o *Orchestrator) retry(ctx context.Context, r *run) error {
	var env messaging.Envelope
	if err := json.Unmarshal(r.inst.LastCommand.Envelope, &env); err != nil {
		return fmt.Errorf("decode last command: %w", err)
	}
	tag, err := r.tx.Exec(ctx, `UPDATE outbox SET published_at = NULL WHERE id = $1`, env.MessageID)
	if err != nil {
		return fmt.Errorf("requeue outbox: %w", err)
	}
	if tag.RowsAffected() == 0 {
		if err := pgoutbox.Enqueue(ctx, r.tx, r.inst.LastCommand.Destination, env); err != nil {
			return err
		}
	}
	r.inst.Attempts++
	r.setDeadline(o.cfg.CommandTimeout)
	r.log(slog.LevelWarn, "saga command retried", observability.EventSagaCommandRetried,
		slog.String(observability.KeyMessageType, env.Type),
		slog.String(observability.KeyMessageID, env.MessageID.String()),
		slog.Int("attempt", r.inst.Attempts))
	return nil
}
