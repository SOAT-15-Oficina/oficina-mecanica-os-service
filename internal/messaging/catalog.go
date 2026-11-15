package messaging

import (
	"time"

	"github.com/google/uuid"
)

const (
	TypeScheduleDiagnosis  = "execution.schedule-diagnosis"
	TypeDiagnosisScheduled = "execution.diagnosis-scheduled"
	TypeDiagnosisStarted   = "execution.diagnosis-started"
	TypeDiagnosisCompleted = "execution.diagnosis-completed"
	TypeEnqueueRepair      = "execution.enqueue-repair"
	TypeRepairEnqueued     = "execution.repair-enqueued"
	TypeRepairRejected     = "execution.repair-rejected"
	TypeRepairStarted      = "execution.repair-started"
	TypeItemStarted        = "execution.item-started"
	TypeItemFinished       = "execution.item-finished"
	TypeRepairCompleted    = "execution.repair-completed"
	TypeCancelTicket       = "execution.cancel-ticket"
	TypeTicketCanceled     = "execution.ticket-canceled"

	TypeCreateQuote      = "billing.create-quote"
	TypeQuoteSent        = "billing.quote-sent"
	TypeQuoteApproved    = "billing.quote-approved"
	TypeQuoteRejected    = "billing.quote-rejected"
	TypeRequestPayment   = "billing.request-payment"
	TypePaymentRequested = "billing.payment-requested"
	TypePaymentConfirmed = "billing.payment-confirmed"
	TypePaymentFailed    = "billing.payment-failed"
	TypeRefundPayment    = "billing.refund-payment"
	TypePaymentRefunded  = "billing.payment-refunded"
	TypeCancelQuote      = "billing.cancel-quote"
	TypeQuoteCanceled    = "billing.quote-canceled"
)

const (
	ReasonCustomer         = "CLIENTE"
	ReasonDeadline         = "PRAZO"
	ReasonDeclined         = "RECUSADO"
	ReasonExpired          = "EXPIRADO"
	ReasonBayUnavailable   = "BANCADA_INDISPONIVEL"
	ReasonSagaCompensation = "COMPENSACAO"
	ReasonOperatorCanceled = "CANCELADA_PELO_OPERADOR"
)

type Vehicle struct {
	LicensePlate string `json:"license_plate"`
	Brand        string `json:"brand"`
	Model        string `json:"model"`
	Year         int    `json:"year"`
}

type Customer struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

type QuoteItem struct {
	WorkOrderServiceID   uuid.UUID `json:"work_order_service_id"`
	Title                string    `json:"title"`
	PriceCents           int       `json:"price_cents"`
	EstimatedTimeMinutes int       `json:"estimated_time_minutes"`
}

type RepairItem struct {
	WorkOrderServiceID   uuid.UUID `json:"work_order_service_id"`
	Title                string    `json:"title"`
	EstimatedTimeMinutes int       `json:"estimated_time_minutes"`
}

type ScheduleDiagnosis struct {
	Branch        string  `json:"branch"`
	WorkOrderCode string  `json:"work_order_code"`
	Title         string  `json:"title"`
	Vehicle       Vehicle `json:"vehicle"`
}

type DiagnosisScheduled struct {
	QueuePosition int `json:"queue_position"`
}

type DiagnosisStarted struct {
	Technician string    `json:"technician"`
	StartedAt  time.Time `json:"started_at"`
}

type DiagnosisCompleted struct {
	CompletedAt time.Time `json:"completed_at"`
	Notes       string    `json:"notes,omitempty"`
}

type CreateQuote struct {
	WorkOrderCode  string      `json:"work_order_code"`
	Customer       Customer    `json:"customer"`
	Items          []QuoteItem `json:"items"`
	SupplyShortage bool        `json:"supply_shortage"`
}

type QuoteSent struct {
	QuoteID    uuid.UUID `json:"quote_id"`
	TotalCents int       `json:"total_cents"`
	SentAt     time.Time `json:"sent_at"`
}

type QuoteApproved struct {
	QuoteID            uuid.UUID   `json:"quote_id"`
	ApprovedItems      []uuid.UUID `json:"approved_items"`
	RejectedItems      []uuid.UUID `json:"rejected_items"`
	ApprovedTotalCents int         `json:"approved_total_cents"`
}

type QuoteRejected struct {
	QuoteID uuid.UUID `json:"quote_id"`
	Reason  string    `json:"reason"`
}

type RequestPayment struct {
	QuoteID uuid.UUID `json:"quote_id"`
}

type PaymentRequested struct {
	PaymentID   uuid.UUID `json:"payment_id"`
	CheckoutURL string    `json:"checkout_url"`
	AmountCents int       `json:"amount_cents"`
	ExpiresAt   time.Time `json:"expires_at"`
}

type PaymentConfirmed struct {
	PaymentID         uuid.UUID `json:"payment_id"`
	ProviderPaymentID string    `json:"provider_payment_id"`
	AmountCents       int       `json:"amount_cents"`
	ConfirmedAt       time.Time `json:"confirmed_at"`
}

type PaymentFailed struct {
	PaymentID uuid.UUID `json:"payment_id"`
	Reason    string    `json:"reason"`
}

type RefundPayment struct {
	Reason string `json:"reason"`
}

type PaymentRefunded struct {
	PaymentID        uuid.UUID `json:"payment_id"`
	ProviderRefundID string    `json:"provider_refund_id"`
	RefundedAt       time.Time `json:"refunded_at"`
}

type CancelQuote struct {
	Reason string `json:"reason"`
}

type QuoteCanceled struct {
	QuoteID *uuid.UUID `json:"quote_id,omitempty"`
}

type EnqueueRepair struct {
	Branch string       `json:"branch"`
	Items  []RepairItem `json:"items"`
}

type RepairEnqueued struct {
	QueuePosition int `json:"queue_position"`
}

type RepairRejected struct {
	Reason string `json:"reason"`
}

type RepairStarted struct {
	Technician string    `json:"technician"`
	StartedAt  time.Time `json:"started_at"`
}

type ItemStarted struct {
	WorkOrderServiceID uuid.UUID `json:"work_order_service_id"`
	StartedAt          time.Time `json:"started_at"`
}

type ItemFinished struct {
	WorkOrderServiceID uuid.UUID `json:"work_order_service_id"`
	FinishedAt         time.Time `json:"finished_at"`
}

type RepairCompleted struct {
	CompletedAt time.Time `json:"completed_at"`
}

type CancelTicket struct {
	Reason string `json:"reason"`
}

type TicketCanceled struct{}

func NewSagaStart(messageType string, sagaID, workOrderID uuid.UUID, correlationID string, payload any) (Envelope, error) {
	return New(messageType, Envelope{SagaID: sagaID, WorkOrderID: workOrderID, CorrelationID: correlationID}, payload)
}
