package saga

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/SOAT-15-Oficina/oficina-mecanica-os-service/internal/domain"
	"github.com/SOAT-15-Oficina/oficina-mecanica-os-service/internal/messaging"
	"github.com/SOAT-15-Oficina/oficina-mecanica-os-service/internal/messaging/pgoutbox"
	"github.com/SOAT-15-Oficina/oficina-mecanica-os-service/internal/observability"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func (f *fixture) advance(d time.Duration) int {
	f.t.Helper()
	f.clock = f.clock.Add(d)
	n, err := f.o.Tick(f.ctx)
	require.NoError(f.t, err)
	return n
}

func TestTickResendsLastCommandWithSameMessageIDUntilMaxAttemptsThenCompensates(t *testing.T) {
	email := "ana@example.com"
	f := newFixture(t, &email)
	original := f.lastCommand()
	require.NoError(t, pgoutbox.NewStore(f.db).MarkPublished(f.ctx, original.MessageID))

	assert.Equal(t, 0, f.advance(10*time.Second))

	assert.Equal(t, 1, f.advance(30*time.Second))
	inst := f.saga()
	assert.Equal(t, 2, inst.Attempts)
	assert.Equal(t, original.MessageID, f.lastCommand().MessageID)
	pending, err := pgoutbox.NewStore(f.db).FetchPending(f.ctx, 10)
	require.NoError(t, err)
	require.Len(t, pending, 1)
	assert.Equal(t, original.MessageID, pending[0].Envelope.MessageID)

	f.exec(`DELETE FROM outbox`)
	f.advance(31 * time.Second)
	assert.Equal(t, 3, f.saga().Attempts)
	assert.Equal(t, []string{messaging.TypeScheduleDiagnosis}, f.outboxTypes())

	f.advance(31 * time.Second)
	inst = f.saga()
	assert.Equal(t, domain.SagaCompensating, inst.Status)
	assert.Equal(t, ReasonNoResponse, *inst.FailureReason)
	assert.Equal(t, messaging.TypeCancelTicket, f.lastCommand().Type)

	for range 3 {
		f.advance(31 * time.Second)
	}
	inst = f.saga()
	assert.Equal(t, domain.SagaFailed, inst.Status)
	assert.Nil(t, inst.DeadlineAt)
	assert.Equal(t, 0, f.advance(time.Hour))
}

func TestApprovalDeadlineIsTreatedAsRejectionWithPrazo(t *testing.T) {
	email := "ana@example.com"
	f := newFixture(t, &email)
	f.toAwaitApproval()

	f.advance(f.o.cfg.ApprovalTimeout + time.Second)
	inst := f.saga()
	assert.Equal(t, messaging.ReasonDeadline, *inst.FailureReason)
	assert.Equal(t, []string{messaging.TypeCancelQuote, messaging.TypeCancelTicket}, inst.PendingCompensations)
}

func TestPaymentDeadlineIsTreatedAsExpiredPayment(t *testing.T) {
	email := "ana@example.com"
	f := newFixture(t, &email)
	f.toAwaitPayment()

	f.advance(f.o.cfg.PaymentTimeout + time.Second)
	inst := f.saga()
	assert.Equal(t, messaging.ReasonExpired, *inst.FailureReason)
	assert.Equal(t, messaging.TypeCancelQuote, f.lastCommand().Type)
}

func TestTickClearsDeadlineWithoutCommand(t *testing.T) {
	email := "ana@example.com"
	f := newFixture(t, &email)
	f.exec(`UPDATE saga_instances SET last_command = NULL, current_step = 'AWAIT_DIAGNOSIS'`)

	assert.Equal(t, 1, f.advance(time.Minute))
	assert.Nil(t, f.saga().DeadlineAt)
}

func TestRunDeadlinesStopsWithContext(t *testing.T) {
	email := "ana@example.com"
	f := newFixture(t, &email)
	f.o.cfg.Tick = 10 * time.Millisecond
	f.clock = f.clock.Add(time.Minute)

	ctx, cancel := context.WithTimeout(f.ctx, 100*time.Millisecond)
	defer cancel()
	f.o.RunDeadlines(ctx)
	assert.Equal(t, 2, f.saga().Attempts)
}

func TestDuplicateMessageIsProcessedOnce(t *testing.T) {
	email := "ana@example.com"
	f := newFixture(t, &email)
	f.deliver(messaging.TypeDiagnosisScheduled, messaging.DiagnosisScheduled{})
	f.deliver(messaging.TypeDiagnosisStarted, messaging.DiagnosisStarted{})
	env := f.deliver(messaging.TypeDiagnosisCompleted, messaging.DiagnosisCompleted{})
	version := f.saga().Version

	require.NoError(t, f.o.Handle(f.ctx, env))
	assert.Equal(t, version, f.saga().Version)
	assert.Equal(t, []string{messaging.TypeScheduleDiagnosis, messaging.TypeCreateQuote}, f.outboxTypes())
}

func TestOutOfOrderAndForeignMessagesAreIgnoredButMarkedProcessed(t *testing.T) {
	var buf bytes.Buffer
	t.Setenv("DD_ENV", "test")
	email := "ana@example.com"
	f := newFixture(t, &email)
	f.ctx = observability.WithLogger(f.ctx, observability.New(&buf))
	version := f.saga().Version

	early := f.deliver(messaging.TypeQuoteSent, messaging.QuoteSent{QuoteID: uuid.New()})
	f.deliver("execution.unknown", nil)
	assert.Equal(t, version, f.saga().Version)
	assert.Equal(t, domain.WorkOrderStatusReceived, f.status())

	var processed bool
	require.NoError(t, f.db.QueryRow(f.ctx, `SELECT EXISTS (SELECT 1 FROM processed_messages WHERE message_id = $1)`, early.MessageID).Scan(&processed))
	assert.True(t, processed)

	foreign, err := messaging.NewSagaStart(messaging.TypeDiagnosisScheduled, uuid.New(), f.wo.ID, "r", nil)
	require.NoError(t, err)
	require.NoError(t, f.o.Handle(f.ctx, foreign))
	missing, err := messaging.NewSagaStart(messaging.TypeDiagnosisScheduled, uuid.New(), uuid.New(), "r", nil)
	require.NoError(t, err)
	require.NoError(t, f.o.Handle(f.ctx, missing))
	assert.Equal(t, StepScheduleDiagnosis, f.saga().CurrentStep)

	f.deliver(messaging.TypeTicketCanceled, messaging.TicketCanceled{})
	assert.Equal(t, domain.SagaStarted, f.saga().Status)

	assert.Contains(t, buf.String(), `"event":"saga.message_ignored"`)
	assert.Contains(t, buf.String(), `"message_type":"billing.quote-sent"`)
	assert.Contains(t, buf.String(), `"saga_id":"`+f.sagaID.String()+`"`)
}

func TestCompletedSagaIgnoresLateMessages(t *testing.T) {
	email := "ana@example.com"
	f := newFixture(t, &email)
	f.toEnqueueRepair()
	f.deliver(messaging.TypeRepairEnqueued, messaging.RepairEnqueued{})
	f.deliver(messaging.TypeRepairStarted, messaging.RepairStarted{})
	f.deliver(messaging.TypeRepairCompleted, messaging.RepairCompleted{})
	version := f.saga().Version

	f.deliver(messaging.TypeRepairStarted, messaging.RepairStarted{})
	assert.Equal(t, version, f.saga().Version)
}

func TestSaveSagaDetectsConcurrentUpdate(t *testing.T) {
	email := "ana@example.com"
	f := newFixture(t, &email)
	inst := f.saga()
	f.exec(`UPDATE saga_instances SET version = version + 1`)

	err := pgx.BeginFunc(f.ctx, f.db, func(tx pgx.Tx) error {
		return saveSaga(f.ctx, tx, inst, f.clock)
	})
	assert.ErrorIs(t, err, ErrStaleSaga)
}

func TestHandleReturnsErrorOnInvalidTransitionSoTheMessageIsRetried(t *testing.T) {
	email := "ana@example.com"
	f := newFixture(t, &email)
	f.toEnqueueRepair()
	f.deliver(messaging.TypeRepairEnqueued, messaging.RepairEnqueued{})
	f.exec(`UPDATE work_orders SET status = 'RECEBIDA'`)

	err := f.o.Handle(f.ctx, f.envelope(messaging.TypeRepairStarted, messaging.RepairStarted{}))
	assert.ErrorIs(t, err, domain.ErrInvalidStatusTransition)
}
