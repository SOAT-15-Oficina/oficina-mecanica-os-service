package saga

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/SOAT-15-Oficina/oficina-mecanica-os-service/internal/config"
	"github.com/SOAT-15-Oficina/oficina-mecanica-os-service/internal/database"
	"github.com/SOAT-15-Oficina/oficina-mecanica-os-service/internal/domain"
	"github.com/SOAT-15-Oficina/oficina-mecanica-os-service/internal/messaging"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

const (
	executionInbox = "http://localstack:4566/000000000000/execution-inbox.fifo"
	billingInbox   = "http://localstack:4566/000000000000/billing-inbox.fifo"
)

type recordingNotifier struct {
	transitions []domain.WorkOrderStatus
}

func (n *recordingNotifier) NotifyTransition(_ context.Context, wo *domain.WorkOrder, _ domain.WorkOrderStatus) {
	n.transitions = append(n.transitions, wo.Status)
}

type fixture struct {
	t        *testing.T
	ctx      context.Context
	db       *pgxpool.Pool
	o        *Orchestrator
	notifier *recordingNotifier
	clock    time.Time
	wo       *domain.WorkOrder
	sagaID   uuid.UUID
	itemID   uuid.UUID
	supplyID uuid.UUID
}

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if os.Getenv("DATABASE_HOST") == "" {
		t.Skip("DATABASE_HOST nao definido")
	}
	db, err := database.NewConnection(&config.DatabaseConfig{
		User: os.Getenv("DATABASE_USER"), Password: os.Getenv("DATABASE_PASSWORD"),
		Host: os.Getenv("DATABASE_HOST"), Port: os.Getenv("DATABASE_PORT"), Name: os.Getenv("DATABASE_NAME"),
	})
	require.NoError(t, err)
	ctx := context.Background()
	_, err = db.Exec(ctx, `DROP SCHEMA public CASCADE`)
	require.NoError(t, err)
	_, err = db.Exec(ctx, `CREATE SCHEMA public`)
	require.NoError(t, err)
	require.NoError(t, database.RunMigrations(db))
	t.Cleanup(db.Close)
	return db
}

func newFixture(t *testing.T, email *string) *fixture {
	t.Helper()
	db := testPool(t)
	f := &fixture{t: t, ctx: context.Background(), db: db, notifier: &recordingNotifier{}, clock: time.Date(2026, 11, 11, 10, 0, 0, 0, time.UTC)}
	f.o = New(db, Config{
		ExecutionInbox:  executionInbox,
		BillingInbox:    billingInbox,
		CommandTimeout:  30 * time.Second,
		ApprovalTimeout: time.Hour,
		PaymentTimeout:  2 * time.Hour,
		MaxAttempts:     3,
	}, f.notifier)
	f.o.now = func() time.Time { return f.clock }

	userID, customerID, vehicleID, serviceID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	f.supplyID = uuid.New()
	f.exec(`INSERT INTO users (id, username, password_hash, role, created_at, updated_at) VALUES ($1, 'op', 'x', 'admin', now(), now())`, userID)
	f.exec(`INSERT INTO customers (id, name, document, document_type, email, created_at, updated_at) VALUES ($1, 'Ana', '12345678909', 'CPF', $2, now(), now())`, customerID, email)
	f.exec(`INSERT INTO vehicles (id, customer_id, license_plate, brand, model, year, created_at, updated_at) VALUES ($1, $2, 'ABC1D23', 'Fiat', 'Uno', 2020, now(), now())`, vehicleID, customerID)
	f.exec(`INSERT INTO services (id, title, price_cents, estimated_time_minutes, created_at, updated_at) VALUES ($1, 'Troca de oleo', 10000, 30, now(), now())`, serviceID)
	f.exec(`INSERT INTO supplies (id, title, type, price_cents, stock_quantity, created_at, updated_at) VALUES ($1, 'Oleo', 'PECA', 2500, 10, now(), now())`, f.supplyID)

	f.wo = &domain.WorkOrder{
		ID: uuid.New(), Code: "OS-20261111-0001", Title: "Revisao", CustomerID: customerID, VehicleID: vehicleID,
		OpenedByUserID: userID, Status: domain.WorkOrderStatusReceived, ReceivedAt: f.clock,
	}
	require.NoError(t, f.o.Open(f.ctx, f.wo, &domain.Vehicle{LicensePlate: "ABC1D23", Brand: "Fiat", Model: "Uno", Year: 2020}))
	f.sagaID = f.saga().ID

	f.itemID = uuid.New()
	f.exec(`INSERT INTO work_order_services (id, work_order_id, service_id, service_title_snapshot, service_price_cents_snapshot, service_estimated_time_minutes_snapshot, created_at, updated_at)
		VALUES ($1, $2, $3, 'Troca de oleo', 10000, 30, now(), now())`, f.itemID, f.wo.ID, serviceID)
	f.exec(`INSERT INTO work_order_service_supplies (id, work_order_service_id, supply_id, supply_title_snapshot, supply_price_cents_snapshot, supply_quantity, created_at, updated_at)
		VALUES ($1, $2, $3, 'Oleo', 2500, 4, now(), now())`, uuid.New(), f.itemID, f.supplyID)
	return f
}

func (f *fixture) exec(sql string, args ...any) {
	f.t.Helper()
	_, err := f.db.Exec(f.ctx, sql, args...)
	require.NoError(f.t, err)
}

func (f *fixture) envelope(messageType string, payload any) messaging.Envelope {
	f.t.Helper()
	env, err := messaging.NewSagaStart(messageType, f.sagaID, f.wo.ID, "req", payload)
	require.NoError(f.t, err)
	return env
}

func (f *fixture) deliver(messageType string, payload any) messaging.Envelope {
	f.t.Helper()
	env := f.envelope(messageType, payload)
	require.NoError(f.t, f.o.Handle(f.ctx, env))
	return env
}

func (f *fixture) saga() *domain.SagaInstance {
	f.t.Helper()
	inst, err := f.o.Get(f.ctx, f.wo.ID)
	require.NoError(f.t, err)
	return inst
}

func (f *fixture) status() domain.WorkOrderStatus {
	f.t.Helper()
	var status domain.WorkOrderStatus
	require.NoError(f.t, f.db.QueryRow(f.ctx, `SELECT status FROM work_orders WHERE id = $1`, f.wo.ID).Scan(&status))
	return status
}

func (f *fixture) outboxTypes() []string {
	f.t.Helper()
	rows, err := f.db.Query(f.ctx, `SELECT message_type FROM outbox ORDER BY created_at, id`)
	require.NoError(f.t, err)
	defer rows.Close()
	var out []string
	for rows.Next() {
		var typ string
		require.NoError(f.t, rows.Scan(&typ))
		out = append(out, typ)
	}
	return out
}

func (f *fixture) lastCommand() messaging.Envelope {
	f.t.Helper()
	inst := f.saga()
	require.NotNil(f.t, inst.LastCommand)
	var env messaging.Envelope
	require.NoError(f.t, json.Unmarshal(inst.LastCommand.Envelope, &env))
	return env
}

func (f *fixture) reservationStatus() string {
	f.t.Helper()
	var status string
	require.NoError(f.t, f.db.QueryRow(f.ctx, `SELECT status FROM supply_reservations WHERE work_order_id = $1`, f.wo.ID).Scan(&status))
	return status
}

func (f *fixture) toAwaitApproval() uuid.UUID {
	f.t.Helper()
	f.deliver(messaging.TypeDiagnosisScheduled, messaging.DiagnosisScheduled{QueuePosition: 1})
	f.deliver(messaging.TypeDiagnosisStarted, messaging.DiagnosisStarted{Technician: "joao", StartedAt: f.clock})
	f.deliver(messaging.TypeDiagnosisCompleted, messaging.DiagnosisCompleted{CompletedAt: f.clock})
	quoteID := uuid.New()
	f.deliver(messaging.TypeQuoteSent, messaging.QuoteSent{QuoteID: quoteID, TotalCents: 20000, SentAt: f.clock})
	return quoteID
}

func (f *fixture) toAwaitPayment() {
	f.t.Helper()
	quoteID := f.toAwaitApproval()
	f.deliver(messaging.TypeQuoteApproved, messaging.QuoteApproved{QuoteID: quoteID, ApprovedItems: []uuid.UUID{f.itemID}, ApprovedTotalCents: 20000})
	f.deliver(messaging.TypePaymentRequested, messaging.PaymentRequested{PaymentID: uuid.New(), CheckoutURL: "http://mp/checkout/1", AmountCents: 20000})
}

func (f *fixture) toEnqueueRepair() {
	f.t.Helper()
	f.toAwaitPayment()
	f.deliver(messaging.TypePaymentConfirmed, messaging.PaymentConfirmed{PaymentID: uuid.New(), ProviderPaymentID: "123", AmountCents: 20000, ConfirmedAt: f.clock})
}
