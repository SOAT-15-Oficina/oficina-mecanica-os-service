package saga

import (
	"testing"

	"github.com/SOAT-15-Oficina/oficina-mecanica-os-service/internal/application"
	"github.com/SOAT-15-Oficina/oficina-mecanica-os-service/internal/domain"
	"github.com/SOAT-15-Oficina/oficina-mecanica-os-service/internal/messaging"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCaseAQuoteRejectedCancelsTicketAndCancelsWorkOrder(t *testing.T) {
	email := "ana@example.com"
	f := newFixture(t, &email)
	quoteID := f.toAwaitApproval()

	f.deliver(messaging.TypeQuoteRejected, messaging.QuoteRejected{QuoteID: quoteID, Reason: messaging.ReasonCustomer})
	inst := f.saga()
	assert.Equal(t, domain.SagaCompensating, inst.Status)
	assert.Equal(t, []string{messaging.TypeCancelTicket}, inst.PendingCompensations)
	assert.Equal(t, messaging.ReasonCustomer, *inst.FailureReason)
	assert.Equal(t, messaging.TypeCancelTicket, f.lastCommand().Type)
	assert.Equal(t, executionInbox, inst.LastCommand.Destination)

	var approval string
	require.NoError(t, f.db.QueryRow(f.ctx, `SELECT approval_status FROM work_order_services WHERE id = $1`, f.itemID).Scan(&approval))
	assert.Equal(t, "REPROVADO", approval)

	f.deliver(messaging.TypeTicketCanceled, messaging.TicketCanceled{})
	assert.Equal(t, domain.WorkOrderStatusCanceled, f.status())
	assert.Equal(t, domain.SagaCompensated, f.saga().Status)
	assert.Nil(t, f.saga().DeadlineAt)
}

func TestQuoteApprovedWithoutItemsIsTreatedAsRejection(t *testing.T) {
	email := "ana@example.com"
	f := newFixture(t, &email)
	quoteID := f.toAwaitApproval()

	f.deliver(messaging.TypeQuoteApproved, messaging.QuoteApproved{QuoteID: quoteID})
	assert.Equal(t, []string{messaging.TypeCancelTicket}, f.saga().PendingCompensations)
}

func TestCaseBPaymentFailedCancelsQuoteThenTicket(t *testing.T) {
	email := "ana@example.com"
	f := newFixture(t, &email)
	f.toAwaitPayment()

	f.deliver(messaging.TypePaymentFailed, messaging.PaymentFailed{Reason: messaging.ReasonDeclined})
	inst := f.saga()
	assert.Equal(t, []string{messaging.TypeCancelQuote, messaging.TypeCancelTicket}, inst.PendingCompensations)
	assert.Equal(t, messaging.TypeCancelQuote, f.lastCommand().Type)
	assert.Equal(t, f.wo.ID, f.lastCommand().WorkOrderID)

	f.deliver(messaging.TypeTicketCanceled, messaging.TicketCanceled{})
	assert.Equal(t, messaging.TypeCancelQuote, f.lastCommand().Type, "confirmacao fora de ordem e ignorada")

	f.deliver(messaging.TypeQuoteCanceled, messaging.QuoteCanceled{})
	assert.Equal(t, messaging.TypeCancelTicket, f.lastCommand().Type)
	assert.Equal(t, domain.WorkOrderStatusWaitingPayment, f.status())

	f.deliver(messaging.TypeTicketCanceled, messaging.TicketCanceled{})
	assert.Equal(t, domain.WorkOrderStatusCanceled, f.status())
	assert.Equal(t, domain.SagaCompensated, f.saga().Status)
}

func TestCaseCRepairRejectedRefundsReleasesReservationAndCancels(t *testing.T) {
	email := "ana@example.com"
	f := newFixture(t, &email)
	f.toEnqueueRepair()

	f.deliver(messaging.TypeRepairRejected, messaging.RepairRejected{Reason: messaging.ReasonBayUnavailable})
	inst := f.saga()
	assert.Equal(t, []string{messaging.TypeRefundPayment, messaging.TypeCancelTicket}, inst.PendingCompensations)
	assert.Equal(t, messaging.TypeRefundPayment, f.lastCommand().Type)
	assert.Equal(t, billingInbox, inst.LastCommand.Destination)
	assert.Equal(t, "LIBERADA", f.reservationStatus())

	f.deliver(messaging.TypePaymentRefunded, messaging.PaymentRefunded{})
	assert.Equal(t, messaging.TypeCancelTicket, f.lastCommand().Type)

	f.deliver(messaging.TypeTicketCanceled, messaging.TicketCanceled{})
	assert.Equal(t, domain.WorkOrderStatusCanceled, f.status())
	assert.Equal(t, domain.SagaCompensated, f.saga().Status)

	var stock int
	require.NoError(t, f.db.QueryRow(f.ctx, `SELECT stock_quantity FROM supplies WHERE id = $1`, f.supplyID).Scan(&stock))
	assert.Equal(t, 10, stock)
}

func TestDiagnosisWithoutItemsCompensatesWithReason(t *testing.T) {
	email := "ana@example.com"
	f := newFixture(t, &email)
	f.exec(`DELETE FROM work_order_service_supplies`)
	f.exec(`DELETE FROM work_order_services`)

	f.deliver(messaging.TypeDiagnosisScheduled, messaging.DiagnosisScheduled{})
	f.deliver(messaging.TypeDiagnosisCompleted, messaging.DiagnosisCompleted{CompletedAt: f.clock})

	inst := f.saga()
	assert.Equal(t, domain.SagaCompensating, inst.Status)
	assert.Equal(t, ReasonNoItems, *inst.FailureReason)
	assert.Equal(t, []string{messaging.TypeCancelQuote, messaging.TypeCancelTicket}, inst.PendingCompensations)
	assert.Equal(t, domain.WorkOrderStatusInDiagnosis, f.status())
}

func TestCustomerWithoutEmailCompensatesInsteadOfStalling(t *testing.T) {
	f := newFixture(t, nil)

	f.deliver(messaging.TypeDiagnosisScheduled, messaging.DiagnosisScheduled{})
	f.deliver(messaging.TypeDiagnosisStarted, messaging.DiagnosisStarted{})
	f.deliver(messaging.TypeDiagnosisCompleted, messaging.DiagnosisCompleted{})

	inst := f.saga()
	assert.Equal(t, ReasonNoEmail, *inst.FailureReason)
	assert.NotContains(t, f.outboxTypes(), messaging.TypeCreateQuote)
}

func TestOperatorCancelBeforePivotCompensatesAndAfterPivotIsRejected(t *testing.T) {
	email := "ana@example.com"
	f := newFixture(t, &email)
	f.toEnqueueRepair()

	inst, err := f.o.Cancel(f.ctx, f.wo.ID)
	require.NoError(t, err)
	assert.Equal(t, messaging.ReasonOperatorCanceled, *inst.FailureReason)
	assert.Equal(t, []string{messaging.TypeRefundPayment, messaging.TypeCancelTicket}, inst.PendingCompensations)

	_, err = f.o.Cancel(f.ctx, f.wo.ID)
	assert.ErrorIs(t, err, application.ErrSagaNotCancelable)

	g := newFixture(t, &email)
	g.toEnqueueRepair()
	g.deliver(messaging.TypeRepairEnqueued, messaging.RepairEnqueued{})
	_, err = g.o.Cancel(g.ctx, g.wo.ID)
	assert.ErrorIs(t, err, application.ErrSagaNotCancelable)

	_, err = g.o.Cancel(g.ctx, uuid.New())
	assert.ErrorIs(t, err, application.ErrNotFound)
}

func TestOperatorCancelRightAfterOpeningOnlyCancelsTicket(t *testing.T) {
	email := "ana@example.com"
	f := newFixture(t, &email)

	inst, err := f.o.Cancel(f.ctx, f.wo.ID)
	require.NoError(t, err)
	assert.Equal(t, []string{messaging.TypeCancelTicket}, inst.PendingCompensations)

	f.deliver(messaging.TypeDiagnosisScheduled, messaging.DiagnosisScheduled{})
	assert.Equal(t, StepCompensate, f.saga().CurrentStep)

	f.deliver(messaging.TypeTicketCanceled, messaging.TicketCanceled{})
	assert.Equal(t, domain.WorkOrderStatusCanceled, f.status())
}

func TestLatePaymentDuringCompensationAddsRefund(t *testing.T) {
	email := "ana@example.com"
	f := newFixture(t, &email)
	f.toAwaitPayment()

	_, err := f.o.Cancel(f.ctx, f.wo.ID)
	require.NoError(t, err)
	f.deliver(messaging.TypePaymentConfirmed, messaging.PaymentConfirmed{})
	assert.Equal(t, []string{messaging.TypeCancelQuote, messaging.TypeRefundPayment, messaging.TypeCancelTicket}, f.saga().PendingCompensations)

	f.deliver(messaging.TypePaymentConfirmed, messaging.PaymentConfirmed{})
	assert.Len(t, f.saga().PendingCompensations, 3)

	f.deliver(messaging.TypeQuoteCanceled, messaging.QuoteCanceled{})
	assert.Equal(t, messaging.TypeRefundPayment, f.lastCommand().Type)
}

func TestPlanCompensation(t *testing.T) {
	inst := &domain.SagaInstance{}
	assert.Equal(t, []string{messaging.TypeCancelTicket}, planCompensation(inst, false))

	inst.Complete(StepAwaitDiagnosis)
	assert.Equal(t, []string{messaging.TypeCancelQuote, messaging.TypeCancelTicket}, planCompensation(inst, false))
	assert.Equal(t, []string{messaging.TypeCancelTicket}, planCompensation(inst, true))

	inst.Complete(StepAwaitPayment)
	assert.Equal(t, []string{messaging.TypeRefundPayment, messaging.TypeCancelTicket}, planCompensation(inst, false))
}
