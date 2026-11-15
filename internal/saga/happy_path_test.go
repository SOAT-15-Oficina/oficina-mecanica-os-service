package saga

import (
	"encoding/json"
	"testing"

	"github.com/SOAT-15-Oficina/oficina-mecanica-os-service/internal/domain"
	"github.com/SOAT-15-Oficina/oficina-mecanica-os-service/internal/messaging"
	"github.com/SOAT-15-Oficina/oficina-mecanica-os-service/internal/repository"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOpenStartsSagaWithScheduleDiagnosisInTheSameTransaction(t *testing.T) {
	email := "ana@example.com"
	f := newFixture(t, &email)

	inst := f.saga()
	assert.Equal(t, domain.SagaStarted, inst.Status)
	assert.Equal(t, StepScheduleDiagnosis, inst.CurrentStep)
	assert.Equal(t, 1, inst.Attempts)
	require.NotNil(t, inst.DeadlineAt)
	assert.Equal(t, executionInbox, inst.LastCommand.Destination)
	assert.Equal(t, []string{messaging.TypeScheduleDiagnosis}, f.outboxTypes())

	cmd := f.lastCommand()
	assert.Equal(t, f.sagaID, cmd.SagaID)
	var payload messaging.ScheduleDiagnosis
	require.NoError(t, cmd.DecodePayload(&payload))
	assert.Equal(t, "MATRIZ", payload.Branch)
	assert.Equal(t, "ABC1D23", payload.Vehicle.LicensePlate)
	assert.Equal(t, domain.WorkOrderStatusReceived, f.status())
}

func TestHappyPathReachesFinalizadaAndCompletesSaga(t *testing.T) {
	email := "ana@example.com"
	f := newFixture(t, &email)

	f.deliver(messaging.TypeDiagnosisScheduled, messaging.DiagnosisScheduled{QueuePosition: 1})
	assert.Equal(t, StepAwaitDiagnosis, f.saga().CurrentStep)
	assert.Nil(t, f.saga().DeadlineAt)

	f.deliver(messaging.TypeDiagnosisStarted, messaging.DiagnosisStarted{Technician: "joao", StartedAt: f.clock})
	assert.Equal(t, domain.WorkOrderStatusInDiagnosis, f.status())

	f.deliver(messaging.TypeDiagnosisCompleted, messaging.DiagnosisCompleted{CompletedAt: f.clock})
	cmd := f.lastCommand()
	assert.Equal(t, messaging.TypeCreateQuote, cmd.Type)
	var quote messaging.CreateQuote
	require.NoError(t, cmd.DecodePayload(&quote))
	assert.Equal(t, "ana@example.com", quote.Customer.Email)
	require.Len(t, quote.Items, 1)
	assert.Equal(t, 20000, quote.Items[0].PriceCents)
	assert.False(t, quote.SupplyShortage)

	quoteID := uuid.New()
	f.deliver(messaging.TypeQuoteSent, messaging.QuoteSent{QuoteID: quoteID, TotalCents: 20000, SentAt: f.clock})
	assert.Equal(t, domain.WorkOrderStatusWaitingApproval, f.status())
	assert.True(t, f.clock.Add(f.o.cfg.ApprovalTimeout).Equal(*f.saga().DeadlineAt))

	f.deliver(messaging.TypeQuoteApproved, messaging.QuoteApproved{QuoteID: quoteID, ApprovedItems: []uuid.UUID{f.itemID}, ApprovedTotalCents: 20000})
	assert.Equal(t, domain.WorkOrderStatusWaitingPayment, f.status())
	request := f.lastCommand()
	assert.Equal(t, messaging.TypeRequestPayment, request.Type)
	var rp messaging.RequestPayment
	require.NoError(t, request.DecodePayload(&rp))
	assert.Equal(t, quoteID, rp.QuoteID)

	f.deliver(messaging.TypePaymentRequested, messaging.PaymentRequested{CheckoutURL: "http://mp/checkout/1"})
	var paymentURL string
	require.NoError(t, f.db.QueryRow(f.ctx, `SELECT payment_url FROM work_orders WHERE id = $1`, f.wo.ID).Scan(&paymentURL))
	assert.Equal(t, "http://mp/checkout/1", paymentURL)
	assert.Equal(t, StepAwaitPayment, f.saga().CurrentStep)

	f.deliver(messaging.TypePaymentConfirmed, messaging.PaymentConfirmed{ProviderPaymentID: "1", AmountCents: 20000})
	assert.Equal(t, domain.WorkOrderStatusApproved, f.status())
	assert.Equal(t, "RESERVADA", f.reservationStatus())
	enqueue := f.lastCommand()
	assert.Equal(t, messaging.TypeEnqueueRepair, enqueue.Type)
	var er messaging.EnqueueRepair
	require.NoError(t, enqueue.DecodePayload(&er))
	require.Len(t, er.Items, 1)
	assert.Equal(t, f.itemID, er.Items[0].WorkOrderServiceID)

	f.deliver(messaging.TypeRepairEnqueued, messaging.RepairEnqueued{QueuePosition: 1})
	assert.True(t, f.saga().HasCompleted(StepEnqueueRepair))

	f.deliver(messaging.TypeRepairStarted, messaging.RepairStarted{Technician: "joao", StartedAt: f.clock})
	assert.Equal(t, domain.WorkOrderStatusInProgress, f.status())

	f.deliver(messaging.TypeItemStarted, messaging.ItemStarted{WorkOrderServiceID: f.itemID, StartedAt: f.clock})
	f.deliver(messaging.TypeItemFinished, messaging.ItemFinished{WorkOrderServiceID: f.itemID, FinishedAt: f.clock.Add(45 * 60e9)})
	assert.Equal(t, "CONSUMIDA", f.reservationStatus())
	var stock int
	require.NoError(t, f.db.QueryRow(f.ctx, `SELECT stock_quantity FROM supplies WHERE id = $1`, f.supplyID).Scan(&stock))
	assert.Equal(t, 6, stock)

	f.deliver(messaging.TypeRepairCompleted, messaging.RepairCompleted{CompletedAt: f.clock})
	assert.Equal(t, domain.WorkOrderStatusFinished, f.status())
	inst := f.saga()
	assert.Equal(t, domain.SagaCompleted, inst.Status)
	assert.Equal(t, StepDone, inst.CurrentStep)

	avg, err := repository.NewWorkshopServiceRepository(f.db).GetAvgExecutionTime(f.ctx, domain.AvgExecutionTimeFilters{})
	require.NoError(t, err)
	require.Len(t, avg, 1)
	assert.InDelta(t, 45, avg[0].AvgRealTimeMinutes, 0.01)

	require.NoError(t, f.o.Deliver(f.ctx, f.wo.ID))
	assert.Equal(t, domain.WorkOrderStatusDelivered, f.status())
	assert.ErrorIs(t, f.o.Deliver(f.ctx, f.wo.ID), domain.ErrInvalidStatusTransition)

	history, err := repository.NewWorkOrderRepository(f.db).FindStatusHistory(f.ctx, f.wo.ID)
	require.NoError(t, err)
	var path []domain.WorkOrderStatus
	for _, h := range history {
		path = append(path, h.To)
	}
	assert.Equal(t, []domain.WorkOrderStatus{
		domain.WorkOrderStatusReceived, domain.WorkOrderStatusInDiagnosis, domain.WorkOrderStatusWaitingApproval,
		domain.WorkOrderStatusWaitingPayment, domain.WorkOrderStatusApproved, domain.WorkOrderStatusInProgress,
		domain.WorkOrderStatusFinished, domain.WorkOrderStatusDelivered,
	}, path)
	assert.Equal(t, messaging.TypeQuoteApproved, history[3].Cause)
	assert.NotNil(t, history[3].MessageID)
	assert.Equal(t, CauseDeliver, history[7].Cause)
	assert.Len(t, f.notifier.transitions, 7)
}

func TestDeliverRejectsUnknownAndUnfinishedWorkOrders(t *testing.T) {
	email := "ana@example.com"
	f := newFixture(t, &email)

	assert.ErrorIs(t, f.o.Deliver(f.ctx, f.wo.ID), domain.ErrInvalidStatusTransition)
	assert.Error(t, f.o.Deliver(f.ctx, uuid.New()))
}

func TestRepairCompletedRightAfterRepairStartedWithoutItems(t *testing.T) {
	email := "ana@example.com"
	f := newFixture(t, &email)
	f.toEnqueueRepair()
	f.deliver(messaging.TypeRepairEnqueued, messaging.RepairEnqueued{QueuePosition: 1})
	f.deliver(messaging.TypeRepairCompleted, messaging.RepairCompleted{CompletedAt: f.clock})

	assert.Equal(t, domain.WorkOrderStatusFinished, f.status())
	assert.Equal(t, "CONSUMIDA", f.reservationStatus())
}

func TestSagaViewSerializesState(t *testing.T) {
	email := "ana@example.com"
	f := newFixture(t, &email)

	body, err := json.Marshal(f.saga())
	require.NoError(t, err)
	assert.Contains(t, string(body), `"current_step":"SCHEDULE_DIAGNOSIS"`)
	assert.Contains(t, string(body), `"destination":"`+executionInbox+`"`)
}
