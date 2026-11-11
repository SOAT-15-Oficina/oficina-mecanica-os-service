package saga

import (
	"context"
	"log/slog"
	"slices"

	"github.com/SOAT-15-Oficina/oficina-mecanica-os-service/internal/domain"
	"github.com/SOAT-15-Oficina/oficina-mecanica-os-service/internal/messaging"
	"github.com/SOAT-15-Oficina/oficina-mecanica-os-service/internal/observability"
)

type handlerFunc func(ctx context.Context, r *run) (bool, error)

type route struct {
	steps        []string
	forward      handlerFunc
	compensating handlerFunc
}

var routes = map[string]route{
	messaging.TypeDiagnosisScheduled: {steps: []string{StepScheduleDiagnosis}, forward: onDiagnosisScheduled},
	messaging.TypeDiagnosisStarted:   {steps: []string{StepAwaitDiagnosis}, forward: onDiagnosisStarted},
	messaging.TypeDiagnosisCompleted: {steps: []string{StepAwaitDiagnosis}, forward: onDiagnosisCompleted},
	messaging.TypeQuoteSent:          {steps: []string{StepCreateQuote}, forward: onQuoteSent},
	messaging.TypeQuoteApproved:      {steps: []string{StepAwaitApproval}, forward: onQuoteApproved},
	messaging.TypeQuoteRejected:      {steps: []string{StepAwaitApproval}, forward: onQuoteRejected},
	messaging.TypePaymentRequested:   {steps: []string{StepRequestPayment}, forward: onPaymentRequested},
	messaging.TypePaymentConfirmed:   {steps: []string{StepAwaitPayment}, forward: onPaymentConfirmed, compensating: onLatePayment},
	messaging.TypePaymentFailed:      {steps: []string{StepRequestPayment, StepAwaitPayment}, forward: onPaymentFailed},
	messaging.TypeRepairEnqueued:     {steps: []string{StepEnqueueRepair}, forward: onRepairEnqueued},
	messaging.TypeRepairRejected:     {steps: []string{StepEnqueueRepair}, forward: onRepairRejected},
	messaging.TypeRepairStarted:      {steps: []string{StepAwaitRepair}, forward: onRepairStarted},
	messaging.TypeItemStarted:        {steps: []string{StepAwaitRepair}, forward: onItemStarted},
	messaging.TypeItemFinished:       {steps: []string{StepAwaitRepair}, forward: onItemFinished},
	messaging.TypeRepairCompleted:    {steps: []string{StepAwaitRepair}, forward: onRepairCompleted},
	messaging.TypeTicketCanceled:     {compensating: onCompensationConfirmed},
	messaging.TypeQuoteCanceled:      {compensating: onCompensationConfirmed},
	messaging.TypePaymentRefunded:    {compensating: onCompensationConfirmed},
}

func dispatch(ctx context.Context, r *run, env messaging.Envelope) (bool, error) {
	rt, ok := routes[env.Type]
	if !ok {
		return false, nil
	}
	switch r.inst.Status {
	case domain.SagaStarted:
		if rt.forward == nil || !slices.Contains(rt.steps, r.inst.CurrentStep) {
			return false, nil
		}
		return rt.forward(ctx, r)
	case domain.SagaCompensating:
		if rt.compensating == nil {
			return false, nil
		}
		return rt.compensating(ctx, r)
	default:
		return false, nil
	}
}

func decode[T any](r *run) (T, error) {
	var payload T
	err := r.cause.DecodePayload(&payload)
	return payload, err
}

func onDiagnosisScheduled(_ context.Context, r *run) (bool, error) {
	r.completeStep(StepScheduleDiagnosis)
	r.await(StepAwaitDiagnosis, 0)
	return true, nil
}

func onDiagnosisStarted(ctx context.Context, r *run) (bool, error) {
	return true, r.transition(ctx, domain.WorkOrderStatusInDiagnosis)
}

func onDiagnosisCompleted(ctx context.Context, r *run) (bool, error) {
	if err := r.transition(ctx, domain.WorkOrderStatusInDiagnosis); err != nil {
		return false, err
	}
	q, err := loadQuoteContext(ctx, r.tx, r.inst.WorkOrderID)
	if err != nil {
		return false, err
	}
	r.completeStep(StepAwaitDiagnosis)
	if len(q.items) == 0 {
		return true, startCompensation(ctx, r, ReasonNoItems, false)
	}
	if q.customerEmail == "" {
		return true, startCompensation(ctx, r, ReasonNoEmail, false)
	}
	if err := setEstimatedTotal(ctx, r.tx, r.inst.WorkOrderID, quoteTotal(q.items), r.now); err != nil {
		return false, err
	}
	return true, r.send(ctx, StepCreateQuote, r.o.cfg.BillingInbox, messaging.TypeCreateQuote, messaging.CreateQuote{
		WorkOrderCode:  q.code,
		Customer:       messaging.Customer{Name: q.customerName, Email: q.customerEmail},
		Items:          q.items,
		SupplyShortage: q.shortage,
	})
}

func onQuoteSent(ctx context.Context, r *run) (bool, error) {
	payload, err := decode[messaging.QuoteSent](r)
	if err != nil {
		return false, err
	}
	quoteID := payload.QuoteID
	r.inst.QuoteID = &quoteID
	if err := r.transition(ctx, domain.WorkOrderStatusWaitingApproval); err != nil {
		return false, err
	}
	r.completeStep(StepCreateQuote)
	r.await(StepAwaitApproval, r.o.cfg.ApprovalTimeout)
	return true, nil
}

func onQuoteApproved(ctx context.Context, r *run) (bool, error) {
	payload, err := decode[messaging.QuoteApproved](r)
	if err != nil {
		return false, err
	}
	if len(payload.ApprovedItems) == 0 {
		if err := rejectPendingItems(ctx, r.tx, r.inst.WorkOrderID, r.now); err != nil {
			return false, err
		}
		return true, startCompensation(ctx, r, messaging.ReasonCustomer, true)
	}
	if payload.QuoteID != [16]byte{} {
		quoteID := payload.QuoteID
		r.inst.QuoteID = &quoteID
	}
	if err := applyApproval(ctx, r.tx, r.inst.WorkOrderID, payload.ApprovedItems, payload.ApprovedTotalCents, r.now); err != nil {
		return false, err
	}
	if err := r.transition(ctx, domain.WorkOrderStatusWaitingPayment); err != nil {
		return false, err
	}
	r.completeStep(StepAwaitApproval)
	request := messaging.RequestPayment{}
	if r.inst.QuoteID != nil {
		request.QuoteID = *r.inst.QuoteID
	}
	return true, r.send(ctx, StepRequestPayment, r.o.cfg.BillingInbox, messaging.TypeRequestPayment, request)
}

func onQuoteRejected(ctx context.Context, r *run) (bool, error) {
	payload, err := decode[messaging.QuoteRejected](r)
	if err != nil {
		return false, err
	}
	if err := rejectPendingItems(ctx, r.tx, r.inst.WorkOrderID, r.now); err != nil {
		return false, err
	}
	return true, startCompensation(ctx, r, reasonOr(payload.Reason, messaging.ReasonCustomer), true)
}

func onPaymentRequested(ctx context.Context, r *run) (bool, error) {
	payload, err := decode[messaging.PaymentRequested](r)
	if err != nil {
		return false, err
	}
	if err := setPaymentURL(ctx, r.tx, r.inst.WorkOrderID, payload.CheckoutURL, r.now); err != nil {
		return false, err
	}
	r.completeStep(StepRequestPayment)
	r.await(StepAwaitPayment, r.o.cfg.PaymentTimeout)
	return true, nil
}

func onPaymentConfirmed(ctx context.Context, r *run) (bool, error) {
	if err := r.transition(ctx, domain.WorkOrderStatusApproved); err != nil {
		return false, err
	}
	r.completeStep(StepAwaitPayment)
	if err := reserveSupplies(ctx, r.tx, r.inst.WorkOrderID, r.now); err != nil {
		return false, err
	}
	r.completeStep(StepReserveSupplies)

	var branch string
	if err := r.tx.QueryRow(ctx, `SELECT branch FROM work_orders WHERE id = $1`, r.inst.WorkOrderID).Scan(&branch); err != nil {
		return false, err
	}
	items, err := approvedRepairItems(ctx, r.tx, r.inst.WorkOrderID)
	if err != nil {
		return false, err
	}
	return true, r.send(ctx, StepEnqueueRepair, r.o.cfg.ExecutionInbox, messaging.TypeEnqueueRepair, messaging.EnqueueRepair{Branch: branch, Items: items})
}

func onPaymentFailed(ctx context.Context, r *run) (bool, error) {
	payload, err := decode[messaging.PaymentFailed](r)
	if err != nil {
		return false, err
	}
	return true, startCompensation(ctx, r, reasonOr(payload.Reason, messaging.ReasonDeclined), false)
}

func onRepairEnqueued(_ context.Context, r *run) (bool, error) {
	r.completeStep(StepEnqueueRepair)
	r.await(StepAwaitRepair, 0)
	return true, nil
}

func onRepairRejected(ctx context.Context, r *run) (bool, error) {
	payload, err := decode[messaging.RepairRejected](r)
	if err != nil {
		return false, err
	}
	return true, startCompensation(ctx, r, reasonOr(payload.Reason, messaging.ReasonBayUnavailable), false)
}

func onRepairStarted(ctx context.Context, r *run) (bool, error) {
	return true, r.transition(ctx, domain.WorkOrderStatusInProgress)
}

func onItemStarted(ctx context.Context, r *run) (bool, error) {
	payload, err := decode[messaging.ItemStarted](r)
	if err != nil {
		return false, err
	}
	return true, markItemStarted(ctx, r.tx, r.inst.WorkOrderID, payload.WorkOrderServiceID, timeOr(payload.StartedAt, r.now))
}

func onItemFinished(ctx context.Context, r *run) (bool, error) {
	payload, err := decode[messaging.ItemFinished](r)
	if err != nil {
		return false, err
	}
	if err := markItemFinished(ctx, r.tx, r.inst.WorkOrderID, payload.WorkOrderServiceID, timeOr(payload.FinishedAt, r.now)); err != nil {
		return false, err
	}
	item := payload.WorkOrderServiceID
	return true, consumeSupplies(ctx, r.tx, r.inst.WorkOrderID, &item, r.now)
}

func onRepairCompleted(ctx context.Context, r *run) (bool, error) {
	if err := r.transition(ctx, domain.WorkOrderStatusInProgress); err != nil {
		return false, err
	}
	if err := r.transition(ctx, domain.WorkOrderStatusFinished); err != nil {
		return false, err
	}
	if err := consumeSupplies(ctx, r.tx, r.inst.WorkOrderID, nil, r.now); err != nil {
		return false, err
	}
	r.completeStep(StepAwaitRepair)
	r.inst.Status = domain.SagaCompleted
	r.await(StepDone, 0)
	r.log(slog.LevelInfo, "saga completed", observability.EventSagaCompleted)
	return true, nil
}

func reasonOr(reason, fallback string) string {
	if reason == "" {
		return fallback
	}
	return reason
}
