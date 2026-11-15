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
	"github.com/SOAT-15-Oficina/oficina-mecanica-os-service/internal/repository"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	StepScheduleDiagnosis = "SCHEDULE_DIAGNOSIS"
	StepAwaitDiagnosis    = "AWAIT_DIAGNOSIS"
	StepCreateQuote       = "CREATE_QUOTE"
	StepAwaitApproval     = "AWAIT_APPROVAL"
	StepRequestPayment    = "REQUEST_PAYMENT"
	StepAwaitPayment      = "AWAIT_PAYMENT"
	StepReserveSupplies   = "RESERVE_SUPPLIES"
	StepEnqueueRepair     = "ENQUEUE_REPAIR"
	StepAwaitRepair       = "AWAIT_REPAIR"
	StepCompensate        = "COMPENSATE"
	StepDone              = "DONE"
)

const (
	ReasonNoItems    = "SEM_ITENS"
	ReasonNoEmail    = "SEM_EMAIL"
	ReasonNoResponse = "SEM_RESPOSTA"
)

const (
	CauseOpened   = "work_order.opened"
	CauseDeadline = "saga.deadline"
	CauseCancel   = "operator.cancel"
	CauseDeliver  = "operator.deliver"
)

type Config struct {
	ExecutionInbox  string
	BillingInbox    string
	DefaultBranch   string
	CommandTimeout  time.Duration
	ApprovalTimeout time.Duration
	PaymentTimeout  time.Duration
	MaxAttempts     int
	Tick            time.Duration
	TickBatch       int
}

type Notifier interface {
	NotifyTransition(ctx context.Context, workOrder *domain.WorkOrder, previousStatus domain.WorkOrderStatus)
}

type Orchestrator struct {
	db       *pgxpool.Pool
	cfg      Config
	notifier Notifier
	now      func() time.Time
}

func New(db *pgxpool.Pool, cfg Config, notifier Notifier) *Orchestrator {
	if cfg.MaxAttempts <= 0 {
		cfg.MaxAttempts = 5
	}
	if cfg.TickBatch <= 0 {
		cfg.TickBatch = 20
	}
	if cfg.DefaultBranch == "" {
		cfg.DefaultBranch = "MATRIZ"
	}
	return &Orchestrator{db: db, cfg: cfg, notifier: notifier, now: func() time.Time { return time.Now().UTC() }}
}

type logEntry struct {
	level slog.Level
	msg   string
	attrs []slog.Attr
}

type effects struct {
	transitions []transition
	logs        []logEntry
}

type run struct {
	o         *Orchestrator
	tx        pgx.Tx
	inst      *domain.SagaInstance
	cause     messaging.Envelope
	causeName string
	messageID *uuid.UUID
	now       time.Time
	eff       *effects
}

func (o *Orchestrator) newRun(tx pgx.Tx, inst *domain.SagaInstance, cause messaging.Envelope, causeName string, eff *effects) *run {
	r := &run{o: o, tx: tx, inst: inst, cause: cause, causeName: causeName, now: o.now(), eff: eff}
	if cause.MessageID != uuid.Nil {
		id := cause.MessageID
		r.messageID = &id
	}
	return r
}

func (r *run) log(level slog.Level, msg, event string, attrs ...slog.Attr) {
	base := []slog.Attr{
		observability.Event(event),
		slog.String(observability.KeySagaID, r.inst.ID.String()),
		slog.String(observability.KeyWorkOrderID, r.inst.WorkOrderID.String()),
		slog.String(observability.KeyStep, r.inst.CurrentStep),
	}
	if r.cause.Type != "" {
		base = append(base, slog.String(observability.KeyMessageType, r.cause.Type))
	}
	r.eff.logs = append(r.eff.logs, logEntry{level: level, msg: msg, attrs: append(base, attrs...)})
}

func (r *run) transition(ctx context.Context, to domain.WorkOrderStatus) error {
	t, err := recordTransition(ctx, r.tx, r.inst.WorkOrderID, to, r.causeName, r.messageID, r.now)
	if err != nil {
		return err
	}
	if t != nil {
		r.eff.transitions = append(r.eff.transitions, *t)
	}
	return nil
}

func (r *run) setDeadline(d time.Duration) {
	if d <= 0 {
		r.inst.DeadlineAt = nil
		return
	}
	at := r.now.Add(d)
	r.inst.DeadlineAt = &at
}

func (r *run) await(step string, timeout time.Duration) {
	r.inst.CurrentStep = step
	r.inst.LastCommand = nil
	r.inst.Attempts = 0
	r.setDeadline(timeout)
}

func (r *run) completeStep(done string) {
	r.inst.Complete(done)
	r.log(slog.LevelInfo, "saga step completed", observability.EventSagaStepCompleted, slog.String("completed_step", done))
}

func (r *run) send(ctx context.Context, step, destination, messageType string, payload any) error {
	env, err := messaging.New(messageType, r.cause, payload)
	if err != nil {
		return err
	}
	if err := pgoutbox.Enqueue(ctx, r.tx, destination, env); err != nil {
		return err
	}
	body, err := json.Marshal(env)
	if err != nil {
		return err
	}
	r.inst.CurrentStep = step
	r.inst.LastCommand = &domain.SagaCommand{Destination: destination, Envelope: body}
	r.inst.Attempts = 1
	r.setDeadline(r.o.cfg.CommandTimeout)
	return nil
}

func (o *Orchestrator) inTx(ctx context.Context, fn func(tx pgx.Tx, eff *effects) error) error {
	var eff effects
	if err := pgx.BeginFunc(ctx, o.db, func(tx pgx.Tx) error {
		eff = effects{}
		return fn(tx, &eff)
	}); err != nil {
		return err
	}
	o.afterCommit(ctx, eff)
	return nil
}

func (o *Orchestrator) afterCommit(ctx context.Context, eff effects) {
	logger := observability.FromContext(ctx)
	for _, entry := range eff.logs {
		logger.LogAttrs(ctx, entry.level, entry.msg, entry.attrs...)
	}
	for _, t := range eff.transitions {
		logger.LogAttrs(ctx, slog.LevelInfo, "work order status changed",
			observability.Event(observability.EventWorkOrderStatusChanged),
			slog.String(observability.KeyWorkOrderID, t.workOrder.ID.String()),
			slog.String(observability.KeyWorkOrderCode, t.workOrder.Code),
			slog.String(observability.KeyFrom, string(t.from)),
			slog.String(observability.KeyTo, string(t.workOrder.Status)),
			slog.Float64(observability.KeyDurationMS, float64(t.inStatus.Microseconds())/1000))
		if o.notifier != nil {
			wo := t.workOrder
			o.notifier.NotifyTransition(ctx, &wo, t.from)
		}
	}
}

func (o *Orchestrator) Open(ctx context.Context, wo *domain.WorkOrder, vehicle *domain.Vehicle) error {
	if wo.Branch == "" {
		wo.Branch = o.cfg.DefaultBranch
	}
	sagaID, err := uuid.NewV7()
	if err != nil {
		return err
	}

	return o.inTx(ctx, func(tx pgx.Tx, eff *effects) error {
		created, err := repository.InsertWorkOrder(ctx, tx, wo)
		if err != nil {
			return fmt.Errorf("insert work order: %w", err)
		}
		inst := &domain.SagaInstance{ID: sagaID, WorkOrderID: created.ID, Status: domain.SagaStarted}
		cause := messaging.Envelope{SagaID: sagaID, WorkOrderID: created.ID, CorrelationID: observability.RequestID(ctx)}
		r := o.newRun(tx, inst, cause, CauseOpened, eff)

		if err := insertHistory(ctx, tx, created.ID, nil, created.Status, CauseOpened, nil, r.now); err != nil {
			return err
		}
		payload := messaging.ScheduleDiagnosis{Branch: created.Branch, WorkOrderCode: created.Code, Title: created.Title}
		if vehicle != nil {
			payload.Vehicle = messaging.Vehicle{LicensePlate: vehicle.LicensePlate, Brand: vehicle.Brand, Model: vehicle.Model, Year: vehicle.Year}
		}
		if err := r.send(ctx, StepScheduleDiagnosis, o.cfg.ExecutionInbox, messaging.TypeScheduleDiagnosis, payload); err != nil {
			return err
		}
		if err := insertSaga(ctx, tx, inst, r.now); err != nil {
			return err
		}
		r.log(slog.LevelInfo, "saga started", observability.EventSagaStarted)
		return nil
	})
}

func (o *Orchestrator) Handle(ctx context.Context, env messaging.Envelope) error {
	return o.inTx(ctx, func(tx pgx.Tx, eff *effects) error {
		fresh, err := pgoutbox.MarkProcessed(ctx, tx, env)
		if err != nil {
			return err
		}
		inst, err := loadSaga(ctx, tx, env.WorkOrderID)
		if err != nil {
			inst = &domain.SagaInstance{ID: env.SagaID, WorkOrderID: env.WorkOrderID}
			r := o.newRun(tx, inst, env, env.Type, eff)
			r.log(slog.LevelWarn, "saga message ignored", observability.EventSagaMessageIgnored, slog.String(observability.KeyReason, "saga not found"))
			return nil
		}
		r := o.newRun(tx, inst, env, env.Type, eff)
		if !fresh {
			r.log(slog.LevelInfo, "saga message ignored", observability.EventSagaMessageIgnored, slog.String(observability.KeyReason, "duplicate"))
			return nil
		}
		if inst.ID != env.SagaID {
			r.log(slog.LevelWarn, "saga message ignored", observability.EventSagaMessageIgnored, slog.String(observability.KeyReason, "saga id mismatch"))
			return nil
		}

		handled, err := dispatch(ctx, r, env)
		if err != nil {
			return err
		}
		if !handled {
			r.log(slog.LevelInfo, "saga message ignored", observability.EventSagaMessageIgnored, slog.String(observability.KeyReason, "out of step"))
			return nil
		}
		return saveSaga(ctx, tx, inst, r.now)
	})
}

func (o *Orchestrator) Get(ctx context.Context, workOrderID uuid.UUID) (*domain.SagaInstance, error) {
	return loadSaga(ctx, o.db, workOrderID)
}

func (o *Orchestrator) Deliver(ctx context.Context, workOrderID uuid.UUID) error {
	return o.inTx(ctx, func(tx pgx.Tx, eff *effects) error {
		t, err := recordTransition(ctx, tx, workOrderID, domain.WorkOrderStatusDelivered, CauseDeliver, nil, o.now())
		if err != nil {
			return err
		}
		if t == nil {
			return fmt.Errorf("%w: already %s", domain.ErrInvalidStatusTransition, domain.WorkOrderStatusDelivered)
		}
		eff.transitions = append(eff.transitions, *t)
		return nil
	})
}
