package saga

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/SOAT-15-Oficina/oficina-mecanica-os-service/internal/application"
	"github.com/SOAT-15-Oficina/oficina-mecanica-os-service/internal/domain"
	"github.com/SOAT-15-Oficina/oficina-mecanica-os-service/internal/messaging"
	"github.com/SOAT-15-Oficina/oficina-mecanica-os-service/internal/observability"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var confirmationOf = map[string]string{
	messaging.TypeTicketCanceled:  messaging.TypeCancelTicket,
	messaging.TypeQuoteCanceled:   messaging.TypeCancelQuote,
	messaging.TypePaymentRefunded: messaging.TypeRefundPayment,
}

func planCompensation(inst *domain.SagaInstance, quoteClosed bool) []string {
	paid := inst.HasCompleted(StepAwaitPayment)
	quoteRequested := inst.HasCompleted(StepAwaitDiagnosis)

	plan := []string{}
	if paid {
		plan = append(plan, messaging.TypeRefundPayment)
	}
	if quoteRequested && !paid && !quoteClosed {
		plan = append(plan, messaging.TypeCancelQuote)
	}
	return append(plan, messaging.TypeCancelTicket)
}

func startCompensation(ctx context.Context, r *run, reason string, quoteClosed bool) error {
	if r.inst.HasCompleted(StepReserveSupplies) {
		if err := releaseSupplies(ctx, r.tx, r.inst.WorkOrderID, r.now); err != nil {
			return err
		}
	}
	r.inst.Status = domain.SagaCompensating
	r.inst.FailureReason = &reason
	r.inst.PendingCompensations = planCompensation(r.inst, quoteClosed)
	r.log(slog.LevelWarn, "saga compensation started", observability.EventSagaCompensationStarted,
		slog.String(observability.KeyReason, reason),
		slog.Any("pending_compensations", r.inst.PendingCompensations))
	return sendCompensation(ctx, r)
}

func sendCompensation(ctx context.Context, r *run) error {
	reason := ""
	if r.inst.FailureReason != nil {
		reason = *r.inst.FailureReason
	}
	head := r.inst.PendingCompensations[0]
	switch head {
	case messaging.TypeRefundPayment:
		return r.send(ctx, StepCompensate, r.o.cfg.BillingInbox, head, messaging.RefundPayment{Reason: reason})
	case messaging.TypeCancelQuote:
		return r.send(ctx, StepCompensate, r.o.cfg.BillingInbox, head, messaging.CancelQuote{Reason: reason})
	default:
		return r.send(ctx, StepCompensate, r.o.cfg.ExecutionInbox, messaging.TypeCancelTicket, messaging.CancelTicket{Reason: reason})
	}
}

func onCompensationConfirmed(ctx context.Context, r *run) (bool, error) {
	pending := r.inst.PendingCompensations
	if len(pending) == 0 || confirmationOf[r.cause.Type] != pending[0] {
		return false, nil
	}
	r.inst.Complete("COMPENSATED:" + pending[0])
	r.inst.PendingCompensations = pending[1:]
	if len(r.inst.PendingCompensations) > 0 {
		return true, sendCompensation(ctx, r)
	}

	if err := r.transition(ctx, domain.WorkOrderStatusCanceled); err != nil {
		return false, err
	}
	r.inst.Status = domain.SagaCompensated
	r.await(StepDone, 0)
	r.log(slog.LevelInfo, "saga compensated", observability.EventSagaCompensated)
	return true, nil
}

func onLatePayment(ctx context.Context, r *run) (bool, error) {
	pending := r.inst.PendingCompensations
	for _, p := range pending {
		if p == messaging.TypeRefundPayment {
			return false, nil
		}
	}
	r.inst.Complete(StepAwaitPayment)
	if len(pending) == 0 {
		r.inst.PendingCompensations = []string{messaging.TypeRefundPayment}
		return true, sendCompensation(ctx, r)
	}
	r.inst.PendingCompensations = append([]string{pending[0], messaging.TypeRefundPayment}, pending[1:]...)
	return true, nil
}

func onPaymentAfterCompensation(ctx context.Context, r *run) (bool, error) {
	if r.inst.HasCompleted(StepAwaitPayment) {
		return false, nil
	}
	r.inst.Complete(StepAwaitPayment)
	r.inst.Status = domain.SagaCompensating
	r.inst.PendingCompensations = []string{messaging.TypeRefundPayment}
	r.log(slog.LevelWarn, "saga compensation started", observability.EventSagaCompensationStarted,
		slog.String(observability.KeyReason, ReasonLatePayment),
		slog.Any("pending_compensations", r.inst.PendingCompensations))
	return true, sendCompensation(ctx, r)
}

func (o *Orchestrator) Cancel(ctx context.Context, workOrderID uuid.UUID) (*domain.SagaInstance, error) {
	var out *domain.SagaInstance
	err := o.inTx(ctx, func(tx pgx.Tx, eff *effects) error {
		inst, err := loadSaga(ctx, tx, workOrderID)
		if err != nil {
			return err
		}
		if inst.Status != domain.SagaStarted || inst.HasCompleted(StepEnqueueRepair) {
			return application.ErrSagaNotCancelable
		}
		cause := messaging.Envelope{SagaID: inst.ID, WorkOrderID: inst.WorkOrderID, CorrelationID: observability.RequestID(ctx)}
		r := o.newRun(tx, inst, cause, CauseCancel, eff)
		if err := startCompensation(ctx, r, messaging.ReasonOperatorCanceled, false); err != nil {
			return err
		}
		if err := saveSaga(ctx, tx, inst, r.now); err != nil {
			return err
		}
		out = inst
		return nil
	})
	if errors.Is(err, ErrStaleSaga) {
		return nil, application.ErrSagaNotCancelable
	}
	return out, err
}

func timeOr(t, fallback time.Time) time.Time {
	if t.IsZero() {
		return fallback
	}
	return t
}
